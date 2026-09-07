"""Constrained full-text downloader (T04, design.md §9 SSRF constraints).

Original implementation for LuminBuddy. It deliberately fixes the failure
modes of the upstream AutoResearch PR #7 downloader (scheme-only URL checks,
blind redirect following) — none of its code is copied.

Security model:

- Only ``http``/``https`` URLs are accepted.
- Every hop's target is resolved (all A/AAAA records) and each IP is checked
  against a blocklist: loopback, private (RFC1918/ULA), link-local, the
  cloud-metadata address 169.254.169.254 explicitly, unspecified (0.0.0.0,
  ::), multicast, reserved, and broadcast ranges. IP-literal hosts are
  checked the same way *without* DNS.
- The request loop never uses the HTTP library's automatic redirect
  following: redirects are followed manually, hop-by-hop, with full
  re-validation per hop (max 5 hops).
- Size is bounded twice: ``Content-Length`` pre-check plus a streamed byte
  counter with an abort at ``size_limit``.
- ``Content-Type`` must be in the whitelist; ``application/octet-stream`` is
  admitted after a magic-byte sniff (PDF or textual).
- Content hash: SHA-256 of the downloaded bytes, hex-encoded.

TEST INJECTION POINT (deliberate, read this before touching it):
``IpPolicy`` is injectable via the ``ip_policy`` argument of
:func:`constrained_download` so offline tests can run a loopback fixture
server (the test fixture listens on 127.0.0.1, which the production policy
would reject). The ONLY documented use is constructing the download call
with a test policy in tests/ (see tests/conftest.py ``TestOnlyIpPolicy``).
There is no environment variable, config file, or payload field that can
weaken the production policy: ``constrained_download`` with the default
``ip_policy=None`` selects ``build_default_ip_policy()``, and reviewers
should assert that stays true.
"""

from __future__ import annotations

import hashlib
import ipaddress
import socket
import urllib.parse
from dataclasses import dataclass
from typing import Callable

import httpx

#: Hop / size / time bounds (design.md §9; 25 MiB mirrors the design cap).
MAX_REDIRECT_HOPS = 5
DEFAULT_SIZE_LIMIT = 25 * 1024 * 1024
DEFAULT_CONNECT_TIMEOUT_S = 10.0
DEFAULT_READ_TIMEOUT_S = 60.0
#: Per-read call timeout; the streamed download is additionally bounded by the
#: byte cap, so a slowloris body cannot extend the total indefinitely beyond
#: (size_limit / chunk) * read_timeout — deadline_ms on the envelope remains
#: the authoritative end-to-end bound.
CHUNK_SIZE = 64 * 1024

ALLOWED_CONTENT_TYPES: frozenset[str] = frozenset(
    {"application/pdf", "text/plain", "text/markdown"}
)
SNIFFABLE_CONTENT_TYPES: frozenset[str] = frozenset({"application/octet-stream"})

PDF_MAGIC = b"%PDF-"

#: Shared user agent for worker egress (discovery + downloads).
USER_AGENT = (
    "lumin-scholar-worker/0.1 (+https://luminbuddy.example; research discovery)"
)


class DownloadError(Exception):
    """Typed download failure surfaced as an acquisition error_code."""

    def __init__(self, code: str, detail: str) -> None:
        super().__init__(detail)
        self.code = code
        self.detail = detail


@dataclass(frozen=True)
class DownloadResult:
    """Blob-transfer result (contracts.md §4 fetch_full_text outputs)."""

    content: bytes
    content_hash: str
    size_bytes: int
    media_type: str
    content_type_reported: str
    final_url: str
    redirect_hops: int
    looks_like_pdf: bool
    likely_scanned: bool | None

    def to_dict(self) -> dict[str, object]:
        return {
            "content_hash": self.content_hash,
            "size_bytes": self.size_bytes,
            "media_type": self.media_type,
            "content_type_reported": self.content_type_reported,
            "final_url": self.final_url,
            "redirect_hops": self.redirect_hops,
            "looks_like_pdf": self.looks_like_pdf,
            "likely_scanned": self.likely_scanned,
        }


class IpPolicy:
    """Decides whether a resolved IP may be contacted.

    The default policy blocks loopback/private/link-local/metadata/reserved
    ranges. Tests subclass or replace this to admit the loopback fixture
    server; production code paths always use :func:`build_default_ip_policy`.
    """

    def check_ip(self, ip: ipaddress.IPv4Address | ipaddress.IPv6Address) -> None:
        """Raise :class:`DownloadError` when the IP must not be contacted."""
        raise NotImplementedError


def build_default_ip_policy() -> IpPolicy:
    """The production policy. No bypass knobs; tests construct their own."""
    return _DefaultIpPolicy()


class _DefaultIpPolicy(IpPolicy):
    def check_ip(self, ip: ipaddress.IPv4Address | ipaddress.IPv6Address) -> None:
        if isinstance(ip, ipaddress.IPv6Address) and ip.ipv4_mapped:
            ip = ip.ipv4_mapped
        v4 = ip if isinstance(ip, ipaddress.IPv4Address) else None
        v6 = ip if isinstance(ip, ipaddress.IPv6Address) else None

        def bad(reason: str) -> DownloadError:
            return DownloadError(
                "forbidden_target_ip", f"refusing to contact blocked address {ip} ({reason})"
            )

        # Explicit metadata address first (defense in depth: it is inside the
        # link-local range anyway, but call it out by name per design.md §9).
        if v4 is not None and str(v4) == "169.254.169.254":
            raise bad("cloud metadata endpoint")
        if v6 is not None and str(v6).lower() in {
            "fd00:ec2::254",  # AWS EC2 IPv6 metadata endpoint
            "fd00:ec2::253",  # AWS EC2 IPv6 metadata (alternate)
        }:
            raise bad("cloud metadata endpoint")
        if ip.is_unspecified:  # 0.0.0.0 / ::
            raise bad("unspecified address")
        if ip.is_loopback:
            raise bad("loopback")
        if ip.is_link_local:
            raise bad("link-local")
        if ip.is_multicast or ip.is_reserved:
            raise bad("multicast/reserved")
        if ip.is_private:
            # RFC1918 (10/8, 172.16/12, 192.168/16), fc00::/7, carrier-grade
            # NAT 100.64/10, benchmarking 198.18/15, broadcast, and the other
            # IANA special-purpose blocks flagged by ipaddress.
            raise bad("private")
        if v4 is not None and not v4.is_global:
            # Final catch-all for any remaining non-global IPv4 special-use
            # range not covered above (e.g. 192.0.0.0/24, 192.0.2.0/24).
            raise bad("non-global IPv4")
        if v6 is not None and not v6.is_global:
            # Same catch-all for IPv6 special-use ranges (e.g. 2001:db8::/32).
            raise bad("non-global IPv6")


def resolve_host_ips(hostname: str) -> list[ipaddress.IPv4Address | ipaddress.IPv6Address]:
    """Resolve all A/AAAA records for a hostname."""
    infos = socket.getaddrinfo(hostname, None, proto=socket.IPPROTO_TCP)
    ips: list[ipaddress.IPv4Address | ipaddress.IPv6Address] = []
    for info in infos:
        address = info[4][0]
        ips.append(ipaddress.ip_address(address))
    return ips


def _validate_url_target(url: str, ip_policy: IpPolicy) -> None:
    """Full URL + DNS validation for one hop (also covers IP literals)."""
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme not in ("http", "https"):
        raise DownloadError(
            "invalid_oa_url", f"only http/https URLs are allowed, got scheme {parsed.scheme!r}"
        )
    if not parsed.hostname:
        raise DownloadError("invalid_oa_url", "URL has no host")
    if parsed.username or parsed.password:
        raise DownloadError("invalid_oa_url", "URL must not carry credentials")
    try:
        ipaddress.ip_address(parsed.hostname)
        ips: list[ipaddress.IPv4Address | ipaddress.IPv6Address] = [
            ipaddress.ip_address(parsed.hostname)
        ]
    except ValueError:
        ips = resolve_host_ips(parsed.hostname)
    if not ips:
        raise DownloadError("unresolvable_host", f"no addresses for host {parsed.hostname!r}")
    for ip in ips:
        ip_policy.check_ip(ip)


def sniff_media_type(
    content_type: str | None, head: bytes
) -> str | None:
    """Map the reported Content-Type to an admitted media type, sniffing when
    the server only says ``application/octet-stream``. Returns None to reject."""
    normalized = (content_type or "").split(";", 1)[0].strip().lower()
    if normalized in ALLOWED_CONTENT_TYPES:
        if normalized == "application/pdf":
            return "application/pdf"
        return normalized  # text/plain / text/markdown
    if normalized in SNIFFABLE_CONTENT_TYPES:
        if head.startswith(PDF_MAGIC):
            return "application/pdf"
        if _looks_textual(head):
            return "text/plain"
        return None
    if normalized == "":
        # Server sent no Content-Type: sniff like octet-stream.
        if head.startswith(PDF_MAGIC):
            return "application/pdf"
        if _looks_textual(head):
            return "text/plain"
        return None
    # Anything else the server claims is rejected outright: HTML landing
    # pages, ZIPs, executables, etc. are not admitted by magic-byte guessing.
    return None


def _looks_textual(head: bytes) -> bool:
    """Cheap binary-vs-text heuristic on the first bytes of the body."""
    if not head:
        return False
    sample = head[:4096]
    if b"\x00" in sample:
        return False
    # Count ASCII printable/control-text bytes; a body dominated by other
    # bytes (arbitrary binary, or non-ASCII-heavy encodings) is not admitted
    # as text via sniffing.
    printable = sum(1 for b in sample if 0x20 <= b < 0x7F or b in (9, 10, 13))
    return printable >= 0.9 * max(1, len(sample))


def probe_pdf(content: bytes) -> tuple[bool, bool | None]:
    """PDF magic + cheap scan-vs-text clue probe.

    Returns ``(looks_like_pdf, likely_scanned)``. Full scanned-PDF detection
    (page-count, per-page text extraction) is T05 scope; ``None`` means
    "no clue available at this stage".
    """
    if not content.startswith(PDF_MAGIC):
        return False, None
    # Heuristic clue only: a PDF with no font objects at all and few bytes
    # per page is a scan candidate. Deliberately conservative.
    has_fonts = b"/Font" in content[:65536] or b"/Font" in content[-65536:]
    if not has_fonts and len(content) < 1024 * 1024:
        return True, True
    if not has_fonts:
        return True, True
    return True, None


def constrained_download(
    client: httpx.Client,
    url: str,
    *,
    size_limit: int = DEFAULT_SIZE_LIMIT,
    ip_policy: IpPolicy | None = None,
    connect_timeout_s: float = DEFAULT_CONNECT_TIMEOUT_S,
    read_timeout_s: float = DEFAULT_READ_TIMEOUT_S,
    _hop: int = 0,
) -> DownloadResult:
    """Download ``url`` under the full constraint set, following redirects
    manually with per-hop re-validation.

    Every hop: validate scheme/credentials, resolve DNS, screen every IP, then
    open a streaming response. Redirects close the stream and recurse (bounded
    by MAX_REDIRECT_HOPS); a final response streams through the byte cap with
    SHA-256 computed on the fly, so an oversized body is aborted mid-stream
    instead of being swallowed whole.

    ``client`` must have ``follow_redirects=False`` (enforced).
    """
    policy = ip_policy if ip_policy is not None else build_default_ip_policy()
    if client._transport is not None and client.follow_redirects:  # pragma: no cover
        raise RuntimeError("download client must have follow_redirects=False")
    if _hop > MAX_REDIRECT_HOPS:
        raise DownloadError(
            "too_many_redirects", f"exceeded {MAX_REDIRECT_HOPS} redirect hops"
        )

    _validate_url_target(url, policy)

    timeout = httpx.Timeout(connect_timeout_s, read=read_timeout_s, write=read_timeout_s)
    try:
        with client.stream("GET", url, timeout=timeout) as response:
            if response.is_redirect:
                location = response.headers.get("location")
                if not location:
                    raise DownloadError(
                        "redirect_without_location", f"{url}: redirect lacks Location"
                    )
                next_url = urllib.parse.urljoin(url, location)
                # The stream closes here (context manager) before the next hop.
                return constrained_download(
                    client,
                    next_url,
                    size_limit=size_limit,
                    ip_policy=policy,
                    connect_timeout_s=connect_timeout_s,
                    read_timeout_s=read_timeout_s,
                    _hop=_hop + 1,
                )
            if response.status_code >= 400:
                raise DownloadError(
                    "download_http_error", f"{url}: HTTP {response.status_code}"
                )

            content_length = response.headers.get("Content-Length")
            if content_length is not None and content_length.isdigit():
                if int(content_length) > size_limit:
                    raise DownloadError(
                        "content_too_large",
                        f"Content-Length {content_length} exceeds limit {size_limit}",
                    )

            # Media sniffing needs the first bytes; read them through the same
            # stream so nothing beyond the cap is ever pulled.
            head = bytearray()
            chunks: list[bytes] = []
            hasher = hashlib.sha256()
            total = 0
            for chunk in response.iter_bytes(chunk_size=CHUNK_SIZE):
                total += len(chunk)
                if len(head) < 4096:
                    head.extend(chunk[: 4096 - len(head)])
                if total > size_limit:
                    raise DownloadError(
                        "content_too_large",
                        f"streamed body exceeded limit {size_limit}; "
                        f"aborted after {total} bytes",
                    )
                hasher.update(chunk)
                chunks.append(chunk)
            content = b"".join(chunks)

            media_type = sniff_media_type(response.headers.get("Content-Type"), bytes(head))
            if media_type is None:
                raise DownloadError(
                    "content_type_rejected",
                    f"Content-Type {response.headers.get('Content-Type')!r} is not allowed",
                )

            looks_pdf, likely_scanned = probe_pdf(content)
            return DownloadResult(
                content=content,
                content_hash="sha256:" + hasher.hexdigest(),
                size_bytes=len(content),
                media_type=media_type,
                content_type_reported=response.headers.get("Content-Type") or "",
                final_url=str(response.url),
                redirect_hops=_hop,
                looks_like_pdf=looks_pdf,
                likely_scanned=likely_scanned,
            )
    except httpx.TimeoutException as exc:
        raise DownloadError("download_timeout", f"{url}: timed out: {exc}") from exc
    except httpx.TransportError as exc:
        raise DownloadError("download_unreachable", f"{url}: {exc}") from exc


def make_download_client() -> httpx.Client:
    """Production downloader client: redirects NEVER followed automatically —
    the manual loop in :func:`constrained_download` re-validates every hop."""
    return httpx.Client(
        follow_redirects=False,
        headers={"User-Agent": USER_AGENT},
        timeout=httpx.Timeout(DEFAULT_CONNECT_TIMEOUT_S, read=DEFAULT_READ_TIMEOUT_S),
    )
