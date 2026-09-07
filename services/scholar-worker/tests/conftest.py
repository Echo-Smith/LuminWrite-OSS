"""Shared test fixtures for the Scholar Worker test suite.

Everything here is TEST-ONLY:

- ``TestOnlyIpPolicy`` is the documented SSRF-policy injection point. The
  production downloader always uses ``build_default_ip_policy()``; this
  subclass exists solely so offline tests can talk to a loopback fixture
  server. It never appears in ``src/``, and no env var or payload can select
  it — grep the worker sources to confirm.
- ``fixture_http_server`` spins a real ``http.server`` on 127.0.0.1 to
  exercise redirect chains, size caps, and content-type rules without any
  external network access.
"""

from __future__ import annotations

import ipaddress
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Callable

import httpx
import pytest

from lumin_scholar import downloader


class TestOnlyIpPolicy(downloader.IpPolicy):
    """TEST-ONLY policy: admits exactly the networks passed to the constructor,
    then defers everything else to the production rules.

    Abuse guard: this class lives in ``tests/`` only. The worker sources never
    reference it; the production construction path (``operations.
    default_worker_deps`` → ``downloader.make_download_client`` +
    ``constrained_download``) passes ``ip_policy=None`` which selects
    ``build_default_ip_policy()``. Reviewers should assert that stays true.
    """

    def __init__(self, allowed_networks: list[str]) -> None:
        self._allowed = [ipaddress.ip_network(n) for n in allowed_networks]
        self._default = downloader.build_default_ip_policy()

    def check_ip(self, ip: ipaddress.IPv4Address | ipaddress.IPv6Address) -> None:
        if any(ip in net for net in self._allowed):
            return
        self._default.check_ip(ip)


class _FixtureHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, format: str, *args: Any) -> None:  # noqa: A002
        pass

    def _send(self, status: int, headers: dict[str, str], body: bytes) -> None:
        self.send_response(status)
        # "Connection: close" without Content-Length opts this response into
        # EOF framing (HTTP/1.1 close-delimited body) — used by the streaming
        # overrun test so the cap must abort mid-body.
        close_framed = headers.pop("Connection", None) == "close" and \
            "Content-Length" not in headers and "Transfer-Encoding" not in headers
        for key, value in headers.items():
            self.send_header(key, value)
        if close_framed:
            self.send_header("Connection", "close")
            self.close_connection = True
        elif "Content-Length" not in headers and "Transfer-Encoding" not in headers:
            self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802
        path = self.path.split("?", 1)[0]
        routes = self.server.fixture_routes  # type: ignore[attr-defined]
        handler = routes.get(path)
        if handler is None:
            self._send(404, {"Content-Type": "text/plain"}, b"not found")
            return
        status, headers, body = handler(self)
        self._send(status, headers, body)


class FixtureServer:
    """Route-driven loopback HTTP server for download tests."""

    def __init__(self, routes: dict[str, Callable[[Any], tuple[int, dict[str, str], bytes]]]) -> None:
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), _FixtureHandler)
        self.server.fixture_routes = routes  # type: ignore[attr-defined]
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    @property
    def base_url(self) -> str:
        host, port = self.server.server_address[:2]
        return f"http://{host}:{port}"

    def start(self) -> None:
        self.thread.start()

    def stop(self) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)


@pytest.fixture()
def test_ip_policy() -> TestOnlyIpPolicy:
    """Loopback-admitting policy for fixture-server tests."""
    return TestOnlyIpPolicy(["127.0.0.0/8", "::1/128"])


@pytest.fixture()
def download_client() -> httpx.Client:
    """Downloader client for tests: manual redirects, loopback friendly."""
    return httpx.Client(follow_redirects=False, timeout=5.0)
