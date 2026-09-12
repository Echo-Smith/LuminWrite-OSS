"""Offline tests for the constrained downloader (SSRF guards, size, types).

The fixture HTTP server listens on 127.0.0.1, so all downloader runs use the
TEST-ONLY ``TestOnlyIpPolicy`` (see conftest.py) which admits loopback.
Production-policy behaviour is covered separately:

- ``TestDefaultIpPolicyRules`` checks the production blocklist directly on
  representative addresses (no network).
- ``TestProductionConstructionHasNoBypass`` proves the default construction
  path still refuses loopback targets end-to-end.
"""

from __future__ import annotations

import hashlib
import ipaddress

import httpx
import pytest

from lumin_scholar.downloader import (
    MAX_REDIRECT_HOPS,
    DownloadError,
    constrained_download,
    make_download_client,
    sniff_media_type,
    build_default_ip_policy,
)

TEXT_BODY = b"ground control to major tom\n" * 4
PDF_BODY = b"%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n%%EOF\n"


def _sha(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


@pytest.fixture()
def routes():
    return {}


@pytest.fixture()
def fixture_base(routes):
    from conftest import FixtureServer

    def text(_req):
        return 200, {"Content-Type": "text/plain; charset=utf-8"}, TEXT_BODY

    def pdf(_req):
        return 200, {"Content-Type": "application/pdf"}, PDF_BODY

    def octet_text(_req):
        return 200, {"Content-Type": "application/octet-stream"}, TEXT_BODY

    def octet_binary(_req):
        return 200, {"Content-Type": "application/octet-stream"}, b"\x00\x01\x02binary\x00"

    def html(_req):
        return 200, {"Content-Type": "text/html; charset=utf-8"}, b"<html><body>landing page</body></html>"

    def redirect(base_path):
        def handler(_req):
            return 302, {"Location": base_path}, b""
        return handler

    def redirect_abs(url):
        def handler(_req):
            return 302, {"Location": url}, b""
        return handler

    def huge_header(_req):
        return 200, {"Content-Type": "text/plain", "Content-Length": str(40 * 1024 * 1024)}, b"tiny"

    def stream_huge(_req):
        # No Content-Length: close-delimited body ten times the test cap.
        return 200, {"Content-Type": "text/plain", "Connection": "close"}, TEXT_BODY * 500

    routes.update(
        {
            "/text": text,
            "/pdf": pdf,
            "/octet-text": octet_text,
            "/octet-binary": octet_binary,
            "/html": html,
            "/redirect/ok": redirect("/text"),
            "/redirect/relative": redirect("text"),
            "/redirect/text": text,
            "/redirect/private": redirect_abs("http://10.255.255.1/secret"),
            "/redirect/metadata": redirect_abs("http://169.254.169.254/latest/meta-data/"),
            "/redirect/ftp": redirect_abs("ftp://example.invalid/file.txt"),
            "/redirect/loop/1": redirect("/redirect/loop/2"),
            "/redirect/loop/2": redirect("/redirect/loop/3"),
            "/redirect/loop/3": redirect("/redirect/loop/4"),
            "/redirect/loop/4": redirect("/redirect/loop/5"),
            "/redirect/loop/5": redirect("/redirect/loop/6"),
            "/redirect/loop/6": redirect("/text"),
            "/huge-header": huge_header,
            "/stream-huge": stream_huge,
        }
    )

    server = FixtureServer(routes)
    server.start()
    yield server.base_url
    server.stop()


def _download(client, url, *, size_limit=25 * 1024 * 1024, ip_policy):
    return constrained_download(client, url, size_limit=size_limit, ip_policy=ip_policy)


class TestSuccessPaths:
    def test_plain_text_download_hash_and_size(self, fixture_base, download_client, test_ip_policy):
        result = _download(download_client, fixture_base + "/text", ip_policy=test_ip_policy)
        assert result.content == TEXT_BODY
        assert result.content_hash == _sha(TEXT_BODY)
        assert result.size_bytes == len(TEXT_BODY)
        assert result.media_type == "text/plain"
        assert result.redirect_hops == 0
        assert result.looks_like_pdf is False

    def test_pdf_detected(self, fixture_base, download_client, test_ip_policy):
        result = _download(download_client, fixture_base + "/pdf", ip_policy=test_ip_policy)
        assert result.media_type == "application/pdf"
        assert result.looks_like_pdf is True

    def test_octet_stream_sniffed_to_text(self, fixture_base, download_client, test_ip_policy):
        result = _download(download_client, fixture_base + "/octet-text", ip_policy=test_ip_policy)
        assert result.media_type == "text/plain"

    def test_redirect_chain_followed_and_counted(self, fixture_base, download_client, test_ip_policy):
        result = _download(download_client, fixture_base + "/redirect/ok", ip_policy=test_ip_policy)
        assert result.content == TEXT_BODY
        assert result.redirect_hops == 1
        assert result.final_url.endswith("/text")

    def test_relative_redirect_resolved(self, fixture_base, download_client, test_ip_policy):
        result = _download(download_client, fixture_base + "/redirect/relative", ip_policy=test_ip_policy)
        assert result.content == TEXT_BODY


class TestSSRFRejections:
    def test_redirect_to_private_ip_rejected(self, fixture_base, download_client, test_ip_policy):
        with pytest.raises(DownloadError) as excinfo:
            _download(download_client, fixture_base + "/redirect/private", ip_policy=test_ip_policy)
        assert excinfo.value.code == "forbidden_target_ip"

    def test_redirect_to_metadata_ip_rejected(self, fixture_base, download_client, test_ip_policy):
        with pytest.raises(DownloadError) as excinfo:
            _download(download_client, fixture_base + "/redirect/metadata", ip_policy=test_ip_policy)
        assert excinfo.value.code == "forbidden_target_ip"

    def test_redirect_to_non_http_scheme_rejected(self, fixture_base, download_client, test_ip_policy):
        with pytest.raises(DownloadError) as excinfo:
            _download(download_client, fixture_base + "/redirect/ftp", ip_policy=test_ip_policy)
        assert excinfo.value.code == "invalid_oa_url"

    def test_hop_limit_enforced(self, fixture_base, download_client, test_ip_policy):
        with pytest.raises(DownloadError) as excinfo:
            _download(download_client, fixture_base + "/redirect/loop/1", ip_policy=test_ip_policy)
        assert excinfo.value.code == "too_many_redirects"
        assert MAX_REDIRECT_HOPS == 5

    @pytest.mark.parametrize(
        "bad_url",
        ["file:///etc/passwd", "/etc/passwd", "gopher://host/x"],
    )
    def test_url_scheme_validation(self, download_client, test_ip_policy, bad_url):
        with pytest.raises(DownloadError) as excinfo:
            _download(download_client, bad_url, ip_policy=test_ip_policy)
        assert excinfo.value.code == "invalid_oa_url"

    def test_url_with_credentials_rejected(self, fixture_base, download_client, test_ip_policy):
        bad = fixture_base.replace("http://", "http://user:pass@") + "/text"
        with pytest.raises(DownloadError) as excinfo:
            _download(download_client, bad, ip_policy=test_ip_policy)
        assert excinfo.value.code == "invalid_oa_url"


class TestSizeAndTypeLimits:
    def test_content_length_precheck(self, fixture_base, download_client, test_ip_policy):
        with pytest.raises(DownloadError) as excinfo:
            _download(
                download_client,
                fixture_base + "/huge-header",
                size_limit=1024 * 1024,
                ip_policy=test_ip_policy,
            )
        assert excinfo.value.code == "content_too_large"

    def test_streamed_body_aborted_over_cap(self, fixture_base, download_client, test_ip_policy):
        with pytest.raises(DownloadError) as excinfo:
            _download(
                download_client,
                fixture_base + "/stream-huge",
                size_limit=1000,
                ip_policy=test_ip_policy,
            )
        assert excinfo.value.code == "content_too_large"
        assert "aborted" in excinfo.value.detail

    def test_html_content_type_rejected(self, fixture_base, download_client, test_ip_policy):
        with pytest.raises(DownloadError) as excinfo:
            _download(download_client, fixture_base + "/html", ip_policy=test_ip_policy)
        assert excinfo.value.code == "content_type_rejected"

    def test_binary_octet_stream_rejected(self, fixture_base, download_client, test_ip_policy):
        with pytest.raises(DownloadError) as excinfo:
            _download(download_client, fixture_base + "/octet-binary", ip_policy=test_ip_policy)
        assert excinfo.value.code == "content_type_rejected"


class TestSniffing:
    def test_pdf_magic(self):
        assert sniff_media_type("application/octet-stream", PDF_BODY[:16]) == "application/pdf"

    def test_text_sniff(self):
        assert sniff_media_type("", b"plain words here\n") == "text/plain"

    def test_binary_sniff_rejects(self):
        assert sniff_media_type("application/octet-stream", b"\x00\x01\x02") is None

    def test_charset_parameter_stripped(self):
        assert sniff_media_type("text/plain; charset=utf-8", b"abc") == "text/plain"

    def test_disallowed_type(self):
        assert sniff_media_type("application/x-msdownload", b"MZ") is None


class TestDefaultIpPolicyRules:
    """The production blocklist, checked directly (no network)."""

    @pytest.mark.parametrize(
        "bad_ip",
        [
            "127.0.0.1",
            "127.8.8.8",
            "::1",
            "::ffff:127.0.0.1",  # v6-mapped loopback
            "10.1.2.3",
            "172.16.0.1",
            "172.31.255.255",
            "192.168.1.1",
            "169.254.169.254",  # cloud metadata
            "169.254.1.1",  # link-local
            "fe80::1",
            "fc00::1",  # ULA
            "fd00:ec2::254",  # EC2 IPv6 metadata
            "0.0.0.0",
            "::",
            "224.0.0.1",  # multicast
            "ff02::1",  # multicast v6
            "240.0.0.1",  # reserved
            "255.255.255.255",  # broadcast
            "100.64.0.1",  # carrier-grade NAT (private-use)
            "198.18.0.1",  # benchmarking (private-use)
        ],
    )
    def test_blocked(self, bad_ip):
        with pytest.raises(DownloadError) as excinfo:
            build_default_ip_policy().check_ip(ipaddress.ip_address(bad_ip))
        assert excinfo.value.code == "forbidden_target_ip"

    @pytest.mark.parametrize("public_ip", ["8.8.8.8", "1.1.1.1", "2606:4700::1111"])
    def test_public_allowed(self, public_ip):
        build_default_ip_policy().check_ip(ipaddress.ip_address(public_ip))  # no raise


class TestProductionConstructionHasNoBypass:
    """The default construction path must refuse loopback even though the
    fixture server is listening there — proving production wiring has no
    test-only escape hatch."""

    def test_default_downloader_refuses_loopback(self, fixture_base):
        with pytest.raises(DownloadError) as excinfo:
            constrained_download(make_download_client(), fixture_base + "/text")
        assert excinfo.value.code == "forbidden_target_ip"

    def test_default_downloader_refuses_ip_literal(self):
        with pytest.raises(DownloadError) as excinfo:
            constrained_download(make_download_client(), "http://127.0.0.1:1/x")
        assert excinfo.value.code == "forbidden_target_ip"

    def test_client_never_auto_follows_redirects(self):
        assert make_download_client().follow_redirects is False
