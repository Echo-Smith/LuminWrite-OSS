"""Envelope contracts for the Scholar Worker internal HTTP API.

Implements the unified request/response/error shapes from
``specs/research-review/contracts.md`` §4:

- Request envelope: ``request_id``, ``input_hash``, ``operation_version``,
  ``deadline_ms``, ``payload``. Unknown envelope fields and unknown payload
  fields are rejected.
- Success (HTTP 200): ``request_id``, ``input_hash``, ``outputs``, ``usage``,
  ``warnings``, ``versions``.
- Failure: ``error`` object with ``code``, ``retryable``, optional
  ``retry_after_ms`` and ``outcome_unknown``.

Only the standard library is used; validation is hand-written (no pydantic).
"""

from __future__ import annotations

import hashlib
import json
import re
from dataclasses import dataclass, field
from typing import Any, Mapping

from . import SUPPORTED_OPERATION_VERSION

#: Fixed operation whitelist (contracts.md §4).
OPERATIONS: tuple[str, ...] = (
    "discover",
    "rank",
    "fetch_full_text",
    "parse",
    "read",
)

#: Envelope fields accepted in a request body; anything else is rejected.
REQUEST_FIELDS: frozenset[str] = frozenset(
    {"request_id", "input_hash", "operation_version", "deadline_ms", "payload"}
)

#: Envelope fields of a success response body.
RESPONSE_FIELDS: frozenset[str] = frozenset(
    {"request_id", "input_hash", "outputs", "usage", "warnings", "versions"}
)

_INPUT_HASH_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
_REQUEST_ID_RE = re.compile(r"^[A-Za-z0-9._:-]{1,128}$")
_OPERATION_VERSION_RE = re.compile(r"^[0-9]+(\.[0-9]+)*$")

#: ``deadline_ms`` is absolute Unix epoch milliseconds (UTC). The Go host
#: derives it from its context deadline; the worker compares against its own
#: clock before executing.


class ContractError(Exception):
    """Envelope-level protocol violation mapped to an HTTP error response."""

    def __init__(
        self,
        code: str,
        message: str,
        *,
        http_status: int = 400,
        retryable: bool = False,
        retry_after_ms: int | None = None,
        outcome_unknown: bool = False,
    ) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.http_status = http_status
        self.retryable = retryable
        self.retry_after_ms = retry_after_ms
        self.outcome_unknown = outcome_unknown

    def to_dict(self) -> dict[str, Any]:
        body: dict[str, Any] = {
            "code": self.code,
            "retryable": self.retryable,
            "outcome_unknown": self.outcome_unknown,
            "message": self.message,
        }
        if self.retry_after_ms is not None:
            body["retry_after_ms"] = self.retry_after_ms
        return body


@dataclass(frozen=True)
class Usage:
    """Resource usage reported for one bounded operation."""

    measured: bool
    input_tokens: int
    output_tokens: int
    cost_usd: float | None
    provider: str
    model: str

    def to_dict(self) -> dict[str, Any]:
        return {
            "measured": self.measured,
            "input_tokens": self.input_tokens,
            "output_tokens": self.output_tokens,
            "cost_usd": self.cost_usd,
            "provider": self.provider,
            "model": self.model,
        }


@dataclass(frozen=True)
class OperationRequest:
    """Validated request envelope for one operation."""

    request_id: str
    input_hash: str
    operation_version: str
    deadline_ms: int
    payload: Mapping[str, Any]


@dataclass(frozen=True)
class OperationResponse:
    """Success envelope; echoes ``request_id`` and ``input_hash``."""

    request_id: str
    input_hash: str
    outputs: Mapping[str, Any]
    usage: Usage
    warnings: list[str] = field(default_factory=list)
    versions: Mapping[str, str] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        if self.request_id == "":
            raise ValueError("response request_id must not be empty")
        if not _INPUT_HASH_RE.match(self.input_hash):
            raise ValueError("response input_hash must be sha256:<64 hex chars>")
        return {
            "request_id": self.request_id,
            "input_hash": self.input_hash,
            "outputs": self.outputs,
            "usage": self.usage.to_dict(),
            "warnings": list(self.warnings),
            "versions": dict(self.versions),
        }


@dataclass(frozen=True)
class ErrorResponse:
    """Failure envelope (contracts.md §4)."""

    code: str
    retryable: bool
    outcome_unknown: bool
    retry_after_ms: int | None = None
    message: str = ""

    def to_dict(self) -> dict[str, Any]:
        return ContractError(
            code=self.code,
            message=self.message,
            retryable=self.retryable,
            retry_after_ms=self.retry_after_ms,
            outcome_unknown=self.outcome_unknown,
        ).to_dict()


def parse_request(body: Any, *, operation: str) -> OperationRequest:
    """Validate a decoded JSON body as an operation request envelope.

    Raises :class:`ContractError` on any violation: non-object body, missing or
    malformed fields, unknown fields, or a payload that is not a JSON object.
    """
    if operation not in OPERATIONS:
        raise ContractError(
            "unknown_operation",
            f"operation {operation!r} is not in the whitelist",
            http_status=404,
        )
    if not isinstance(body, dict):
        raise ContractError("invalid_request", "request body must be a JSON object")

    unknown = sorted(set(body) - REQUEST_FIELDS)
    if unknown:
        raise ContractError(
            "invalid_request",
            f"unknown request field(s): {', '.join(unknown)}",
        )

    missing = sorted(REQUEST_FIELDS - set(body))
    if missing:
        raise ContractError(
            "invalid_request",
            f"missing request field(s): {', '.join(missing)}",
        )

    request_id = body["request_id"]
    if not isinstance(request_id, str) or not _REQUEST_ID_RE.match(request_id):
        raise ContractError(
            "invalid_request",
            "request_id must be 1-128 chars of [A-Za-z0-9._:-]",
        )

    input_hash = body["input_hash"]
    if not isinstance(input_hash, str) or not _INPUT_HASH_RE.match(input_hash):
        raise ContractError(
            "invalid_input_hash",
            "input_hash must match sha256:<64 lowercase hex chars>",
        )

    operation_version = body["operation_version"]
    if not isinstance(operation_version, str) or not _OPERATION_VERSION_RE.match(
        operation_version
    ):
        raise ContractError(
            "invalid_request",
            "operation_version must be a dotted numeric version string",
        )
    if operation_version != SUPPORTED_OPERATION_VERSION:
        raise ContractError(
            "unsupported_operation_version",
            f"operation_version {operation_version!r} not supported "
            f"(supported: {SUPPORTED_OPERATION_VERSION})",
            http_status=422,
        )

    deadline_ms = body["deadline_ms"]
    if isinstance(deadline_ms, bool) or not isinstance(deadline_ms, int):
        raise ContractError(
            "invalid_request", "deadline_ms must be an integer (Unix epoch ms)"
        )
    if deadline_ms <= 0:
        raise ContractError(
            "invalid_request", "deadline_ms must be a positive epoch-milliseconds value"
        )

    payload = body["payload"]
    if not isinstance(payload, dict):
        raise ContractError("invalid_request", "payload must be a JSON object")

    return OperationRequest(
        request_id=request_id,
        input_hash=input_hash,
        operation_version=operation_version,
        deadline_ms=deadline_ms,
        payload=payload,
    )


def compute_input_hash(payload: Mapping[str, Any]) -> str:
    """Reference input-hash computation used by tests and the mock tooling.

    Canonical form: UTF-8 of ``json.dumps(payload, sort_keys=True,
    separators=(",", ":"), ensure_ascii=False)``. The Go host must agree on one
    canonicalisation before real (T04+) operations ship; see the T03 notes.
    """
    canonical = json.dumps(
        payload, sort_keys=True, separators=(",", ":"), ensure_ascii=False
    ).encode("utf-8")
    return "sha256:" + hashlib.sha256(canonical).hexdigest()
