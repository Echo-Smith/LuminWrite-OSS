"""HTTP layer for the Scholar Worker.

Framework choice (T03): the standard library ``http.server`` with a small
hand-written router. Rationale: the contract surface is one fixed POST route,
one health probe, and strict envelope validation we implement anyway, so a
zero-dependency server keeps the worker image minimal and lets pytest hit a
real loopback server without extra client libraries.

Routes (private network only; never exposed publicly):
- ``POST /internal/v1/operations/{operation}`` — Bearer-token authenticated.
- ``GET  /healthz`` — liveness probe; performs no model or external calls.

Error status mapping:
- 400 invalid_request / invalid_input_hash / invalid_json (envelope)
- 401 unauthorized (missing header or server without configured token)
- 404 unknown_operation
- 408 deadline_exceeded (retryable)
- 413 payload_too_large
- 422 operation-level payload errors
- 500 internal_error (outcome_unknown=true: the caller must not assume the
  operation was not executed)
"""

from __future__ import annotations

import hmac
import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Callable, Mapping

from . import SUPPORTED_OPERATION_VERSION, __version__
from .contracts import (
    OPERATIONS,
    ContractError,
    ErrorResponse,
    OperationResponse,
    parse_request,
)
from .operations import OperationError, response_versions, run_operation

#: Request body cap. Real payload limits are enforced per operation later;
#: this only bounds the HTTP body before JSON parsing.
DEFAULT_MAX_BODY_BYTES = 8 * 1024 * 1024

_OPERATIONS_PREFIX = "/internal/v1/operations/"


class ScholarWorkerAPI:
    """Transport-independent core: (operation, headers, body) -> (status, body).

    Kept free of ``http.server`` types so tests can call it directly and the
    same logic backs the loopback server and any future ASGI adapter.
    """

    def __init__(
        self,
        token: str | None,
        *,
        now_ms: Callable[[], int] | None = None,
        max_body_bytes: int = DEFAULT_MAX_BODY_BYTES,
        handlers: Mapping[str, Callable[[Mapping[str, Any]], Any]] | None = None,
    ) -> None:
        self._token = token or ""
        self._now_ms = now_ms or (lambda: int(time.time() * 1000))
        self._max_body_bytes = max_body_bytes
        # Optional handler registry override (tests inject offline fakes via
        # operations.build_handlers(WorkerDeps(...))); None = production deps.
        self._handlers = handlers

    # -- health ------------------------------------------------------------
    def healthz(self) -> tuple[int, dict[str, Any]]:
        """Liveness only: no auth dependency, no model calls, no network."""
        return 200, {
            "status": "ok",
            "service": "scholar-worker",
            "worker_version": __version__,
            "operation_version": SUPPORTED_OPERATION_VERSION,
            "operations": list(OPERATIONS),
        }

    # -- operations --------------------------------------------------------
    def handle_operation(
        self,
        operation: str,
        headers: Mapping[str, str],
        body: bytes,
    ) -> tuple[int, dict[str, Any]]:
        if not self._authorize(headers):
            return self._error_response(
                ContractError("unauthorized", "missing or invalid bearer token", http_status=401)
            )

        if operation not in OPERATIONS:
            return self._error_response(
                ContractError(
                    "unknown_operation",
                    f"operation {operation!r} is not in the whitelist",
                    http_status=404,
                )
            )

        if len(body) > self._max_body_bytes:
            return self._error_response(
                ContractError(
                    "payload_too_large",
                    f"request body exceeds {self._max_body_bytes} bytes",
                    http_status=413,
                )
            )

        try:
            decoded = json.loads(body.decode("utf-8")) if body else None
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            return self._error_response(
                ContractError("invalid_json", f"request body is not valid JSON: {exc}")
            )

        try:
            request = parse_request(decoded, operation=operation)
        except ContractError as exc:
            return self._error_response(exc)

        # Deadline is absolute epoch ms; overdue requests are rejected before
        # execution so the caller can retry with a fresh budget.
        if self._now_ms() >= request.deadline_ms:
            return self._error_response(
                ContractError(
                    "deadline_exceeded",
                    "request deadline has already passed",
                    http_status=408,
                    retryable=True,
                )
            )

        try:
            result = run_operation(operation, request.payload, handlers=self._handlers)
        except OperationError as exc:
            return self._error_response(
                ErrorResponse(
                    code=exc.code,
                    retryable=exc.retryable,
                    outcome_unknown=exc.outcome_unknown,
                    retry_after_ms=exc.retry_after_ms,
                    message=exc.message,
                ),
                status=exc.http_status,
            )
        except Exception:  # noqa: BLE001 - converted to a typed error envelope
            # The mock handlers are pure, but a crash here must still tell the
            # caller the outcome is unknown rather than "not executed".
            return self._error_response(
                ErrorResponse(
                    code="internal_error",
                    retryable=False,
                    outcome_unknown=True,
                    message="operation failed inside the worker",
                ),
                status=500,
            )

        response = OperationResponse(
            request_id=request.request_id,
            input_hash=request.input_hash,
            outputs=result.outputs,
            usage=result.usage,
            warnings=result.warnings,
            versions=response_versions(),
        )
        return 200, response.to_dict()

    # -- helpers -----------------------------------------------------------
    def _authorize(self, headers: Mapping[str, str]) -> bool:
        # Fail closed: a worker started without a token accepts nothing.
        if not self._token:
            return False
        header = headers.get("Authorization") or headers.get("authorization") or ""
        scheme, _, credential = header.partition(" ")
        if scheme.lower() != "bearer" or not credential:
            return False
        return hmac.compare_digest(credential.strip(), self._token)

    @staticmethod
    def _error_response(error: ErrorResponse | ContractError, *, status: int | None = None):
        body = error.to_dict() if isinstance(error, ErrorResponse) else {
            "code": error.code,
            "retryable": error.retryable,
            "outcome_unknown": error.outcome_unknown,
            "message": error.message,
            **({"retry_after_ms": error.retry_after_ms} if error.retry_after_ms is not None else {}),
        }
        return (status if status is not None else error.http_status), {"error": body}


def make_http_handler(api: ScholarWorkerAPI) -> type[BaseHTTPRequestHandler]:
    """Build a ``BaseHTTPRequestHandler`` class bound to an API instance."""

    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        server_version = "lumin-scholar/" + __version__

        # Requests come from the Go host, not browsers; keep test output clean.
        def log_message(self, format: str, *args: Any) -> None:  # noqa: A002
            pass

        def _send_json(self, status: int, payload: Mapping[str, Any]) -> None:
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def _read_body(self) -> bytes | None:
            length_header = self.headers.get("Content-Length")
            if length_header is None:
                self._send_json(
                    411,
                    {"error": {"code": "length_required", "retryable": False,
                               "outcome_unknown": False,
                               "message": "Content-Length header is required"}},
                )
                return None
            try:
                length = int(length_header)
            except ValueError:
                length = -1
            if length < 0 or length > api._max_body_bytes:
                self._send_json(
                    413,
                    {"error": {"code": "payload_too_large", "retryable": False,
                               "outcome_unknown": False,
                               "message": "request body exceeds the configured limit"}},
                )
                return None
            return self.rfile.read(length)

        def do_POST(self) -> None:  # noqa: N802 (http.server API)
            path = self.path.split("?", 1)[0]
            if not path.startswith(_OPERATIONS_PREFIX):
                self._send_json(
                    404,
                    {"error": {"code": "unknown_operation", "retryable": False,
                               "outcome_unknown": False,
                               "message": "only /internal/v1/operations/{operation} is served"}},
                )
                return
            operation = path[len(_OPERATIONS_PREFIX):]
            body = self._read_body()
            if body is None:
                return
            status, payload = api.handle_operation(operation, self.headers, body)
            self._send_json(status, payload)

        def do_GET(self) -> None:  # noqa: N802 (http.server API)
            path = self.path.split("?", 1)[0]
            if path == "/healthz":
                status, payload = api.healthz()
                self._send_json(status, payload)
                return
            self._send_json(
                404,
                {"error": {"code": "not_found", "retryable": False,
                           "outcome_unknown": False, "message": "not found"}},
            )

        def do_PUT(self) -> None:  # noqa: N802 (http.server API)
            self._send_json(
                405,
                {"error": {"code": "method_not_allowed", "retryable": False,
                           "outcome_unknown": False,
                           "message": "method not allowed"}},
            )

        # Same rejection for other verbs.
        do_DELETE = do_PUT
        do_PATCH = do_PUT

    return Handler


def make_server(host: str, port: int, api: ScholarWorkerAPI) -> ThreadingHTTPServer:
    """Create (not start) a loopback-capable threaded HTTP server."""
    handler = make_http_handler(api)
    server = ThreadingHTTPServer((host, port), handler)
    server.daemon_threads = True
    return server
