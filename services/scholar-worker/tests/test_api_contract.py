"""Contract tests for the Scholar Worker HTTP API (specs/research-review
contracts.md §4).

Runs against a real loopback server on an ephemeral port; fully offline.
Covers the six required cases plus the health probe:

1. success envelope echo (request_id / input_hash / usage / versions)
2. unknown operation -> 404
3. unknown request field -> 400
4. malformed input_hash -> 400
5. missing / wrong bearer token -> 401
6. duplicate paper_id in rank -> 422
7. expired deadline_ms -> 408 deadline_exceeded (retryable)
8. GET /healthz without auth
"""

from __future__ import annotations

import json
import threading
import time
import urllib.error
import urllib.request
from typing import Any

import pytest

from lumin_scholar.api import ScholarWorkerAPI, make_server
from lumin_scholar.contracts import compute_input_hash

TEST_TOKEN = "test-token-123"

_NOW_MS = lambda: int(time.time() * 1000)  # noqa: E731


def _future_deadline_ms() -> int:
    return _NOW_MS() + 60_000


def _envelope(payload: dict[str, Any], **overrides: Any) -> dict[str, Any]:
    body: dict[str, Any] = {
        "request_id": "req-test-0001",
        "input_hash": compute_input_hash(payload),
        "operation_version": "1",
        "deadline_ms": _future_deadline_ms(),
        "payload": payload,
    }
    body.update(overrides)
    return body


def _request(
    base_url: str,
    path: str,
    *,
    method: str = "POST",
    body: bytes | None = None,
    token: str | None = TEST_TOKEN,
) -> tuple[int, dict[str, Any]]:
    req = urllib.request.Request(base_url + path, data=body, method=method)
    if token is not None:
        req.add_header("Authorization", f"Bearer {token}")
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            payload = resp.read()
            return resp.status, json.loads(payload) if payload else {}
    except urllib.error.HTTPError as exc:
        payload = exc.read()
        return exc.code, json.loads(payload) if payload else {}


def _post_operation(
    base_url: str,
    operation: str,
    envelope: dict[str, Any],
    *,
    token: str | None = TEST_TOKEN,
    raw_body: bytes | None = None,
) -> tuple[int, dict[str, Any]]:
    body = raw_body if raw_body is not None else json.dumps(envelope).encode("utf-8")
    return _request(
        base_url,
        f"/internal/v1/operations/{operation}",
        body=body,
        token=token,
    )


@pytest.fixture()
def base_url():
    api = ScholarWorkerAPI(TEST_TOKEN)
    server = make_server("127.0.0.1", 0, api)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_address[1]}"
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


class TestOperationSuccess:
    def test_discover_returns_mock_records_and_echoes_envelope(self, base_url):
        payload = {"query": "mock materials", "limit": 2}
        status, body = _post_operation(base_url, "discover", _envelope(payload))

        assert status == 200
        assert body["request_id"] == "req-test-0001"
        assert body["input_hash"] == compute_input_hash(payload)
        assert len(body["outputs"]["records"]) == 2
        assert body["outputs"]["records"][0]["paper_id"] == "mock-paper-0001"
        assert body["outputs"]["provider_status"] == [
            {"provider": "mock", "status": "ok", "returned": 2}
        ]
        usage = body["usage"]
        assert usage["measured"] is True
        assert usage["cost_usd"] is None
        assert usage["provider"] == "mock"
        assert usage["input_tokens"] > 0
        assert body["versions"]["operation_version"] == "1"
        assert body["versions"]["worker_version"]
        assert isinstance(body["warnings"], list)


class TestUnknownOperation:
    def test_unknown_operation_rejected(self, base_url):
        status, body = _post_operation(
            base_url, "teleport", _envelope({"query": "x"})
        )
        assert status == 404
        assert body["error"]["code"] == "unknown_operation"
        assert body["error"]["retryable"] is False
        assert body["error"]["outcome_unknown"] is False


class TestUnknownField:
    def test_unknown_envelope_field_rejected(self, base_url):
        envelope = _envelope({"query": "x"})
        envelope["trace_context"] = {"span": "abc"}
        status, body = _post_operation(base_url, "discover", envelope)

        assert status == 400
        assert body["error"]["code"] == "invalid_request"
        assert "trace_context" in body["error"]["message"]

    def test_missing_required_field_rejected(self, base_url):
        envelope = _envelope({"query": "x"})
        del envelope["deadline_ms"]
        status, body = _post_operation(base_url, "discover", envelope)
        assert status == 400
        assert body["error"]["code"] == "invalid_request"

    def test_malformed_json_rejected(self, base_url):
        status, body = _post_operation(
            base_url, "discover", {}, raw_body=b"{not json"
        )
        assert status == 400
        assert body["error"]["code"] == "invalid_json"


class TestBadHash:
    def test_malformed_input_hash_rejected(self, base_url):
        envelope = _envelope({"query": "x"}, input_hash="deadbeef")
        status, body = _post_operation(base_url, "discover", envelope)

        assert status == 400
        assert body["error"]["code"] == "invalid_input_hash"

    def test_uppercase_hash_rejected(self, base_url):
        good = compute_input_hash({"query": "x"})
        envelope = _envelope({"query": "x"}, input_hash=good.upper())
        status, body = _post_operation(base_url, "discover", envelope)

        assert status == 400
        assert body["error"]["code"] == "invalid_input_hash"


class TestAuthentication:
    def test_missing_authorization_header_rejected(self, base_url):
        status, body = _post_operation(
            base_url, "discover", _envelope({"query": "x"}), token=None
        )
        assert status == 401
        assert body["error"]["code"] == "unauthorized"

    def test_wrong_token_rejected(self, base_url):
        status, body = _post_operation(
            base_url, "discover", _envelope({"query": "x"}), token="wrong-token"
        )
        assert status == 401
        assert body["error"]["code"] == "unauthorized"

    def test_worker_without_token_fails_closed(self, base_url):
        api = ScholarWorkerAPI(None)
        headers = {"Authorization": "Bearer anything"}
        status, body = api.handle_operation(
            "discover", headers, json.dumps(_envelope({"query": "x"})).encode()
        )
        assert status == 401
        assert body["error"]["code"] == "unauthorized"


class TestRankDuplicateIds:
    def test_duplicate_paper_id_rejected(self, base_url):
        candidate = {"paper_id": "dup-1", "abstract": "text"}
        payload = {"research_question": "q", "candidates": [candidate, dict(candidate)]}
        status, body = _post_operation(base_url, "rank", _envelope(payload))

        assert status == 422
        assert body["error"]["code"] == "duplicate_paper_id"
        assert body["error"]["retryable"] is False
        assert body["error"]["outcome_unknown"] is False


class TestDeadline:
    def test_expired_deadline_rejected_before_execution(self, base_url):
        envelope = _envelope({"query": "x"}, deadline_ms=_NOW_MS() - 1)
        status, body = _post_operation(base_url, "discover", envelope)

        assert status == 408
        assert body["error"]["code"] == "deadline_exceeded"
        assert body["error"]["retryable"] is True
        assert body["error"]["outcome_unknown"] is False

    def test_non_integer_deadline_rejected(self, base_url):
        envelope = _envelope({"query": "x"}, deadline_ms="soon")
        status, body = _post_operation(base_url, "discover", envelope)
        assert status == 400
        assert body["error"]["code"] == "invalid_request"


class TestHealthz:
    def test_healthz_reports_operations_without_auth(self, base_url):
        status, body = _request(base_url, "/healthz", method="GET", token=None)
        assert status == 200
        assert body["status"] == "ok"
        assert set(body["operations"]) == {
            "discover", "rank", "fetch_full_text", "parse", "read",
        }
