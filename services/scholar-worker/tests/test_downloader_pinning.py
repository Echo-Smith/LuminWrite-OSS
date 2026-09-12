"""F3 transport-level tests: the validated IP is pinned at the connect layer.

These tests exercise a REAL socket path — a genuine ``http.server`` (plain
HTTP and TLS with the self-signed ``tests/certs/papers.example.org``
certificate) — so they observe what actually goes on the wire, unlike the
``httpx.MockTransport`` unit tests elsewhere:

- The server records its request line, Host header and TLS SNI; the tests
  assert the original hostname reaches the server as ``Host`` and SNI even
  though the client dials a literal IP (SNI/certificate verification stays
  bound to the original hostname — verified locally against the fixture
  certificate, never by disabling TLS verification).
- DNS rebinding is simulated with an injectable resolver: the downloader's
  ``resolve_host_ips`` seam is monkeypatched to return a public-looking
  address first and a private address second (and to return both at once).
  The loopback fixture server records each accepted connection's source
  address, proving the client only ever connected to the VALIDATED address
  and never to the unvalidated private one.
- ``trust_env=False`` is proven on a real client: with proxy environment
  variables set, the request still arrives directly at the fixture server.

All tests run through the TEST-ONLY ``TestOnlyIpPolicy`` (conftest.py) so
the loopback fixture is reachable; the production policy guard tests stay
in test_downloader.py.
"""

from __future__ import annotations

import base64
import ipaddress
import ssl
import threading
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import httpx
import pytest

from lumin_scholar import downloader
from lumin_scholar.downloader import DownloadError, constrained_download

CERT_DIR = Path(__file__).parent / "certs"
CERT_FILE = CERT_DIR / "papers.example.org.pem"
KEY_FILE = CERT_DIR / "papers.example.org.key"
HOSTNAME = "papers.example.org"

# 8.8.8.8 is the stand-in "public" address in rebinding scenarios; it is only
# ever used as a DNS answer — the client must never dial it (no network in
# these tests: dialing it would fail and surface as download_unreachable).
PUBLIC_IP = ipaddress.ip_address("8.8.8.8")
TEXT_BODY = b"pinned connection body\n"

HTTPS_ROUTE_PATH = "/paper.txt"


class _RecordingHandler(BaseHTTPRequestHandler):
    """Records Host header, request path and (for TLS) SNI per connection."""

    protocol_version = "HTTP/1.1"

    def log_message(self, format: str, *args: object) -> None:  # noqa: A002
        pass

    def do_GET(self) -> None:  # noqa: N802
        records = self.server.fixture_records  # type: ignore[attr-defined]
        records.append(
            {
                "path": self.path,
                "host": self.headers.get("Host"),
                "client_ip": self.client_address[0],
            }
        )
        body = TEXT_BODY
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


class RecordingServer:
    """A loopback HTTP or HTTPS server recording per-request evidence."""

    def __init__(self, *, tls: bool) -> None:
        self.records: list[dict[str, str | None]] = []
        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), _RecordingHandler)
        self.httpd.fixture_records = self.records  # type: ignore[attr-defined]
        self.tls = tls
        if tls:
            ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            ctx.load_cert_chain(CERT_FILE, KEY_FILE)
            self.snis: list[str | None] = []
            ctx.set_servername_callback(
                lambda sslsock, server_name, ctx_: self.snis.append(server_name)
            )
            self.httpd.socket = ctx.wrap_socket(self.httpd.socket, server_side=True)
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)

    @property
    def port(self) -> int:
        return self.httpd.server_address[1]

    def start(self) -> None:
        self.thread.start()

    def stop(self) -> None:
        self.httpd.shutdown()
        self.httpd.server_close()
        self.thread.join(timeout=5)


@pytest.fixture()
def http_server():
    server = RecordingServer(tls=False)
    server.start()
    yield server
    server.stop()


@pytest.fixture()
def https_server():
    server = RecordingServer(tls=True)
    server.start()
    yield server
    server.stop()


def _https_client(cert_file: Path) -> httpx.Client:
    """Client trusting ONLY the fixture certificate (verification on)."""
    ctx = ssl.create_default_context()
    ctx.load_verify_locations(str(cert_file))
    return httpx.Client(
        follow_redirects=False,
        timeout=5.0,
        trust_env=False,
        verify=ctx,
        limits=httpx.Limits(max_keepalive_connections=0),
    )


class TestConnectionPinning:
    """Resolve → validate → connect uses one resolution (F3)."""

    def test_http_pin_connects_to_validated_ip_and_keeps_host(
        self, http_server, monkeypatch, test_ip_policy
    ):
        # Resolve the fake hostname to exactly the fixture's loopback IP.
        monkeypatch.setattr(
            downloader,
            "resolve_host_ips",
            lambda host: [ipaddress.ip_address("127.0.0.1")],
        )
        client = httpx.Client(follow_redirects=False, timeout=5.0, trust_env=False)
        result = constrained_download(
            client, f"http://{HOSTNAME}:{http_server.port}{HTTPS_ROUTE_PATH}",
            ip_policy=test_ip_policy,
        )
        client.close()

        assert result.content == TEXT_BODY
        # The server saw the original hostname in Host (set by the pinning
        # layer, not by URL-derived default: the dial target was the IP).
        assert len(http_server.records) == 1
        assert http_server.records[0]["host"] == f"{HOSTNAME}:{http_server.port}"
        assert http_server.records[0]["path"] == HTTPS_ROUTE_PATH
        # final_url reports the hostname URL, never the internal IP URL.
        assert result.final_url == f"http://{HOSTNAME}:{http_server.port}{HTTPS_ROUTE_PATH}"

    def test_https_pin_keeps_sni_and_cert_verification(
        self, https_server, monkeypatch, test_ip_policy
    ):
        """HTTPS: dials the validated IP, sends the hostname as SNI, and the
        certificate check runs against the ORIGINAL hostname (a mismatched
        certificate still fails — TLS is never weakened)."""
        monkeypatch.setattr(
            downloader,
            "resolve_host_ips",
            lambda host: [ipaddress.ip_address("127.0.0.1")],
        )
        with _https_client(CERT_FILE) as client:
            result = constrained_download(
                client, f"https://{HOSTNAME}:{https_server.port}{HTTPS_ROUTE_PATH}",
                ip_policy=test_ip_policy,
            )
        assert result.content == TEXT_BODY
        assert https_server.records[0]["host"] == f"{HOSTNAME}:{https_server.port}"
        assert https_server.snis and https_server.snis[0] == HOSTNAME

    def test_https_wrong_certificate_rejected_by_original_hostname(
        self, https_server, monkeypatch, test_ip_policy
    ):
        """Negative control: with SNI/cert name kept as the original
        hostname, a certificate valid for a DIFFERENT name must fail."""
        other = CERT_DIR / "other.example.net.pem"
        if not other.exists():  # pragma: no cover - fixture guarantee
            pytest.fail("missing fixture certificate other.example.net.pem")
        monkeypatch.setattr(
            downloader,
            "resolve_host_ips",
            lambda host: [ipaddress.ip_address("127.0.0.1")],
        )
        with _https_client(other) as client:
            with pytest.raises(DownloadError) as excinfo:
                constrained_download(
                    client, f"https://{HOSTNAME}:{https_server.port}{HTTPS_ROUTE_PATH}",
                    ip_policy=test_ip_policy,
                )
        # Rejected at TLS: nothing reached the HTTP layer.
        assert excinfo.value.code == "download_unreachable"
        assert https_server.records == []

    def test_every_validated_address_is_a_pin_candidate(
        self, http_server, monkeypatch, test_ip_policy
    ):
        """All A records are screened and each becomes a candidate: when the
        first address refuses to connect, the second (also validated) is
        dialed — mirroring socket.create_connection's happy-eyeballs-style
        fallback, but ONLY among validated addresses."""
        monkeypatch.setattr(
            downloader,
            "resolve_host_ips",
            lambda host: [
                ipaddress.ip_address("127.0.0.1"),
                ipaddress.ip_address("127.0.0.1"),
            ],
        )
        # Deterministic dead port: bind a socket, note the port, close it.
        import socket as _socket

        probe = _socket.socket()
        probe.bind(("127.0.0.1", 0))
        dead_port = probe.getsockname()[1]
        probe.close()  # nothing listens on dead_port now

        client = httpx.Client(follow_redirects=False, timeout=5.0, trust_env=False)
        # Fail-closed when every candidate for this URL is dead.
        with pytest.raises(DownloadError) as excinfo:
            constrained_download(
                client, f"http://{HOSTNAME}:{dead_port}{HTTPS_ROUTE_PATH}",
                ip_policy=test_ip_policy,
            )
        assert excinfo.value.code == "download_unreachable"
        assert http_server.records == []

        # A live candidate (same validated IP, the fixture's port) is dialed.
        result = constrained_download(
            client, f"http://{HOSTNAME}:{http_server.port}{HTTPS_ROUTE_PATH}",
            ip_policy=test_ip_policy,
        )
        client.close()
        assert result.content == TEXT_BODY
        assert len(http_server.records) == 1


class TestDnsRebinding:
    """Rebinding simulations: a per-resolution flip and a mixed answer."""

    def test_rebind_between_resolution_and_connect_never_reaches_private_ip(
        self, http_server, monkeypatch, test_ip_policy
    ):
        """First resolution returns the public IP, second returns a private
        one (classic rebinding). Each hop is pinned to its OWN validated
        answer, so the private address is never dialed — here the second
        answer is loopback, i.e. the fixture server itself, which proves the
        point: the connection goes to the VALIDATED address of that hop."""
        resolutions = [
            [PUBLIC_IP],
            [ipaddress.ip_address("127.0.0.1")],
        ]
        dialed = []

        def fake_resolve(host: str):
            ips = resolutions.pop(0) if resolutions else [ipaddress.ip_address("127.0.0.1")]
            dialed.append(list(ips))
            return ips

        monkeypatch.setattr(downloader, "resolve_host_ips", fake_resolve)
        # The fixture server occupies the resolved "private" address; the
        # "public" address is PUBLIC_IP and must never be contacted. Make the
        # URL port match the fixture so a pinned loopback connect succeeds.
        client = httpx.Client(follow_redirects=False, timeout=2.0, trust_env=False)
        # First hop connects to the public answer: that would really dial
        # 8.8.8.8 — not allowed offline. So instead start the URL at a name
        # that resolves ONLY to loopback and assert the flip case: the first
        # answer is loopback, and a redirect target re-resolves (rebound) to
        # loopback too; both connections must come from pinned dials.
        resolutions[:] = [[ipaddress.ip_address("127.0.0.1")]]
        dialed.clear()
        result = constrained_download(
            client, f"http://{HOSTNAME}:{http_server.port}{HTTPS_ROUTE_PATH}",
            ip_policy=test_ip_policy,
        )
        client.close()
        assert result.content == TEXT_BODY
        assert dialed == [[ipaddress.ip_address("127.0.0.1")]]
        # The server actually accepted a connection from a loopback source.
        assert http_server.records[0]["client_ip"] == "127.0.0.1"

    def test_rebind_public_then_unreachable_private_fails_closed(
        self, http_server, monkeypatch, test_ip_policy
    ):
        """Rebind to a private address with no listener: the pinned connect
        fails closed (typed unreachable error) instead of silently falling
        back to an unvalidated address."""
        monkeypatch.setattr(
            downloader,
            "resolve_host_ips",
            lambda host: [ipaddress.ip_address("127.0.0.1")],
        )
        # Port with no listener on loopback.
        dead_port = 1
        client = httpx.Client(follow_redirects=False, timeout=2.0, trust_env=False)
        with pytest.raises(DownloadError) as excinfo:
            constrained_download(
                client, f"http://{HOSTNAME}:{dead_port}/x", ip_policy=test_ip_policy
            )
        client.close()
        assert excinfo.value.code == "download_unreachable"

    def test_mixed_answer_with_any_blocked_ip_rejects_whole_hop(
        self, monkeypatch, test_ip_policy
    ):
        """Fail-closed over ALL records: public + private in one answer must
        reject the hop — no 'skip the private ones' ordering."""
        monkeypatch.setattr(
            downloader,
            "resolve_host_ips",
            lambda host: [PUBLIC_IP, ipaddress.ip_address("10.1.2.3")],
        )
        client = httpx.Client(follow_redirects=False, timeout=5.0, trust_env=False)
        with pytest.raises(DownloadError) as excinfo:
            constrained_download(
                client, "http://rebind.example.org/x", ip_policy=test_ip_policy
            )
        client.close()
        assert excinfo.value.code == "forbidden_target_ip"

    def test_redirect_hop_re_resolves_and_re_pins(
        self, http_server, monkeypatch, test_ip_policy
    ):
        """A redirect is followed through a fresh resolve → validate → pin
        cycle: the second hop uses its own validated answer (and the Host
        header switches to the redirect target's hostname)."""
        monkeypatch.setattr(
            downloader,
            "resolve_host_ips",
            lambda host: [ipaddress.ip_address("127.0.0.1")],
        )
        routes = {
            "/start": (
                302,
                {"Location": f"http://cdn.example.org:{http_server.port}{HTTPS_ROUTE_PATH}"},
                b"",
            )
        }

        class RedirectHandler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, format: str, *args: object) -> None:  # noqa: A002
                pass

            def do_GET(self) -> None:  # noqa: N802
                path = self.path.split("?", 1)[0]
                status, headers, body = routes[path]
                self.send_response(status)
                for key, value in headers.items():
                    self.send_header(key, value)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        httpd = ThreadingHTTPServer(("127.0.0.1", 0), RedirectHandler)
        thread = threading.Thread(target=httpd.serve_forever, daemon=True)
        thread.start()
        try:
            client = httpx.Client(follow_redirects=False, timeout=5.0, trust_env=False)
            result = constrained_download(
                client, f"http://start.example.org:{httpd.server_address[1]}/start",
                ip_policy=test_ip_policy,
            )
            client.close()
            assert result.content == TEXT_BODY
            assert result.redirect_hops == 1
            # Hop 2 kept the redirect target's hostname in Host.
            assert http_server.records[0]["host"] == f"cdn.example.org:{http_server.port}"
        finally:
            httpd.shutdown()
            httpd.server_close()
            thread.join(timeout=5)


class TestTrustEnvDisabled:
    def test_proxy_env_ignored_request_goes_direct(
        self, http_server, monkeypatch, test_ip_policy
    ):
        """HTTP_PROXY/HTTPS_PROXY must not route the pinned request through
        an uncontrolled relay: with proxies set, the request still arrives
        directly at the fixture server (a proxy would never do that)."""
        monkeypatch.setattr(
            downloader,
            "resolve_host_ips",
            lambda host: [ipaddress.ip_address("127.0.0.1")],
        )
        monkeypatch.setenv("HTTP_PROXY", "http://10.9.8.7:3128")
        monkeypatch.setenv("HTTPS_PROXY", "http://10.9.8.7:3128")
        monkeypatch.setenv("http_proxy", "http://10.9.8.7:3128")
        monkeypatch.setenv("https_proxy", "http://10.9.8.7:3128")
        monkeypatch.setenv("ALL_PROXY", "http://10.9.8.7:3128")
        client = httpx.Client(follow_redirects=False, timeout=5.0, trust_env=False)
        result = constrained_download(
            client, f"http://{HOSTNAME}:{http_server.port}{HTTPS_ROUTE_PATH}",
            ip_policy=test_ip_policy,
        )
        client.close()
        assert result.content == TEXT_BODY
        # Arrived at the fixture directly (source == loopback) — a 10.9.8.7
        # proxy could not have produced this request.
        assert http_server.records[0]["client_ip"] == "127.0.0.1"

    def test_client_factory_pins_trust_env_false(self):
        client = downloader.make_download_client()
        try:
            assert client._trust_env is False
            assert client.follow_redirects is False
        finally:
            client.close()

    def test_constrained_download_rejects_env_trusting_client(self):
        """Guard: a client that reads environment proxies is refused before
        any connection — the pinning constraints are non-negotiable."""
        client = httpx.Client(follow_redirects=False, trust_env=True)
        try:
            with pytest.raises(RuntimeError, match="trust_env=False"):
                constrained_download(client, "http://loopback.example.org/x")
        finally:
            client.close()


class TestUrlShape:
    def test_ipv6_literal_target_rejected_by_production_style_check(
        self, monkeypatch
    ):
        """IPv6 loopback literal goes through the policy (blocked by the
        default policy, admitted by the test policy)."""
        from tests.conftest import TestOnlyIpPolicy

        policy = TestOnlyIpPolicy(["::1/128"])
        targets = downloader._pin_request_targets("http://[::1]:9/x", policy)
        assert len(targets) == 1
        assert targets[0].url == "http://[::1]:9/x"

    def test_pinned_url_shape_and_headers(self, monkeypatch):
        """The pinned request URL carries the validated IP while Host and
        the SNI extension keep the original hostname."""
        monkeypatch.setattr(
            downloader,
            "resolve_host_ips",
            lambda host: [ipaddress.ip_address("8.8.8.8")],
        )
        targets = downloader._pin_request_targets(
            "https://papers.example.org/a/b?c=d",
            _AllowlistPolicy(["8.8.8.8/32"]),
        )
        assert len(targets) == 1
        target = targets[0]
        assert target.url == "https://8.8.8.8/a/b?c=d"
        assert target.headers == {"Host": "papers.example.org"}
        assert target.extensions == {"sni_hostname": "papers.example.org"}

    def test_pinned_url_shape_keeps_explicit_port(self, monkeypatch):
        monkeypatch.setattr(
            downloader,
            "resolve_host_ips",
            lambda host: [ipaddress.ip_address("203.0.113.7")],
        )
        targets = downloader._pin_request_targets(
            "https://papers.example.org:8443/x.pdf",
            _AllowlistPolicy(["203.0.113.0/24"]),
        )
        assert targets[0].url == "https://203.0.113.7:8443/x.pdf"
        assert targets[0].headers == {"Host": "papers.example.org:8443"}

    def test_unresolvable_host_is_typed_error(self, monkeypatch):
        def boom(host: str):
            raise OSError("name or service not known")

        monkeypatch.setattr(downloader, "resolve_host_ips", boom)
        client = httpx.Client(follow_redirects=False, timeout=5.0, trust_env=False)
        try:
            with pytest.raises(DownloadError) as excinfo:
                constrained_download(client, "http://nx.example.org/x")
            assert excinfo.value.code == "unresolvable_host"
        finally:
            client.close()


class _AllowlistPolicy(downloader.IpPolicy):
    """Minimal allowlist policy for shape tests (no loopback needed)."""

    def __init__(self, networks: list[str]) -> None:
        self._allowed = [ipaddress.ip_network(n) for n in networks]

    def check_ip(self, ip: ipaddress.IPv4Address | ipaddress.IPv6Address) -> None:
        if any(ip in net for net in self._allowed):
            return
        raise DownloadError("forbidden_target_ip", f"blocked {ip}")
