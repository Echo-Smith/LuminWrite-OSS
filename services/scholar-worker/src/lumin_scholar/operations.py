"""Operation registry and mock implementations for the Scholar Worker.

Each operation is a pure function of its ``payload`` and returns
``OperationResult(outputs, usage, warnings, versions)``. The T03 skeletons are
deterministic, offline mocks: no network access, no model calls, no upstream
AutoResearch code or fixtures (all sample data below is original synthetic
content). Real discovery/fetch (T04) and parsing/reading (T05) will replace
these implementations behind the same contract.
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass, field
from typing import Any, Callable, Mapping

from .contracts import Usage
from . import SUPPORTED_OPERATION_VERSION, __version__

WORKER_VERSION = __version__

PROVIDER_MOCK = "mock"
MODEL_MOCK = "mock-scholar-v0"

MAX_RANK_CANDIDATES = 8
MAX_READ_BLOCKS = 24
MAX_DISCOVER_LIMIT = 50
MAX_FETCH_SIZE_BYTES = 25 * 1024 * 1024  # mirrors the 25 MiB design cap

TEXT_MEDIA_TYPES = ("text/plain", "text/markdown")


class OperationError(Exception):
    """Operation-level failure mapped to an error response by the API layer."""

    def __init__(
        self,
        code: str,
        message: str,
        *,
        http_status: int = 422,
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


@dataclass(frozen=True)
class OperationResult:
    outputs: Mapping[str, Any]
    usage: Usage
    warnings: list[str] = field(default_factory=list)


def _sha256(text: str) -> str:
    return "sha256:" + hashlib.sha256(text.encode("utf-8")).hexdigest()


def _tokens(text: str) -> int:
    """Deterministic pseudo token estimate (mock only; no tokenizer)."""
    return max(1, len(text) // 4)


def _mock_usage(input_text: str, output_text: str) -> Usage:
    return Usage(
        measured=True,
        input_tokens=_tokens(input_text),
        output_tokens=_tokens(output_text),
        cost_usd=None,
        provider=PROVIDER_MOCK,
        model=MODEL_MOCK,
    )


def _require_str(payload: Mapping[str, Any], key: str, *, allow_empty: bool = False) -> str:
    value = payload.get(key)
    if not isinstance(value, str) or (not allow_empty and not value.strip()):
        raise OperationError(
            "invalid_payload",
            f"payload.{key} must be a non-empty string",
            http_status=422,
        )
    return value


def _reject_unknown_keys(obj: Mapping[str, Any], allowed: set[str], where: str) -> None:
    unknown = sorted(set(obj) - allowed)
    if unknown:
        raise OperationError(
            "invalid_payload",
            f"unknown {where} field(s): {', '.join(unknown)}",
            http_status=422,
        )


def _require_int(
    payload: Mapping[str, Any], key: str, *, minimum: int, maximum: int
) -> int:
    value = payload.get(key)
    if isinstance(value, bool) or not isinstance(value, int):
        raise OperationError(
            "invalid_payload", f"payload.{key} must be an integer", http_status=422
        )
    if not minimum <= value <= maximum:
        raise OperationError(
            "invalid_payload",
            f"payload.{key} must be between {minimum} and {maximum}",
            http_status=422,
        )
    return value


# ---------------------------------------------------------------------------
# Original synthetic sample records (mock only; never copied from upstream).
# ---------------------------------------------------------------------------

_MOCK_DISCOVER_RECORDS: tuple[dict[str, Any], ...] = (
    {
        "paper_id": "mock-paper-0001",
        "title": "Synthetic Baseline Study of Mock Material A Under Load",
        "authors": ["A. Author", "B. Builder"],
        "year": 2024,
        "doi": "10.0000/mock.0001",
        "venue": "Journal of Synthetic Results",
        "abstract": "Mock abstract describing a synthetic experiment on material A. "
        "Generated for offline contract tests only.",
        "source": PROVIDER_MOCK,
    },
    {
        "paper_id": "mock-paper-0002",
        "title": "A Placeholder Survey of Fabricated Methods B",
        "authors": ["C. Constructor"],
        "year": 2023,
        "doi": "10.0000/mock.0002",
        "venue": "Transactions on Placeholder Science",
        "abstract": "Mock survey abstract with invented citations. "
        "Never use this record as real evidence.",
        "source": PROVIDER_MOCK,
    },
    {
        "paper_id": "mock-paper-0003",
        "title": "Toy Corpus Notes on Imaginary Alloy C",
        "authors": ["D. Designer", "E. Engineer", "F. Fabricator"],
        "year": 2025,
        "doi": "10.0000/mock.0003",
        "venue": "Proceedings of the Mock Symposium",
        "abstract": "Mock conference abstract about an alloy that does not exist. "
        "Used to exercise ranking and fetching paths offline.",
        "source": PROVIDER_MOCK,
    },
)


def _op_discover(payload: Mapping[str, Any]) -> OperationResult:
    _reject_unknown_keys(payload, {"query", "provider_allowlist", "limit"}, "payload")
    query = _require_str(payload, "query")

    allowlist = payload.get("provider_allowlist", [PROVIDER_MOCK])
    if not isinstance(allowlist, list) or not all(
        isinstance(item, str) and item for item in allowlist
    ):
        raise OperationError(
            "invalid_payload",
            "payload.provider_allowlist must be a list of non-empty strings",
            http_status=422,
        )
    if PROVIDER_MOCK not in allowlist:
        return OperationResult(
            outputs={"records": [], "provider_status": [
                {"provider": p, "status": "not_available", "returned": 0}
                for p in allowlist
            ]},
            usage=_mock_usage(query, ""),
            warnings=["mock worker only serves the 'mock' provider"],
        )

    limit = payload.get("limit", 10)
    if isinstance(limit, bool) or not isinstance(limit, int):
        raise OperationError(
            "invalid_payload", "payload.limit must be an integer", http_status=422
        )
    if not 1 <= limit <= MAX_DISCOVER_LIMIT:
        raise OperationError(
            "invalid_payload",
            f"payload.limit must be between 1 and {MAX_DISCOVER_LIMIT}",
            http_status=422,
        )

    records = list(_MOCK_DISCOVER_RECORDS[:limit])
    outputs = {
        "records": records,
        "provider_status": [
            {"provider": PROVIDER_MOCK, "status": "ok", "returned": len(records)}
        ],
    }
    rendered = "".join(r["paper_id"] for r in records)
    return OperationResult(
        outputs=outputs,
        usage=_mock_usage(query, rendered),
        warnings=["discover is a deterministic mock; records are synthetic"],
    )


def _op_rank(payload: Mapping[str, Any]) -> OperationResult:
    _reject_unknown_keys(payload, {"research_question", "candidates"}, "payload")
    question = _require_str(payload, "research_question")

    candidates = payload.get("candidates")
    if not isinstance(candidates, list) or not candidates:
        raise OperationError(
            "invalid_payload",
            "payload.candidates must be a non-empty list",
            http_status=422,
        )
    if len(candidates) > MAX_RANK_CANDIDATES:
        raise OperationError(
            "invalid_payload",
            f"payload.candidates must hold at most {MAX_RANK_CANDIDATES} entries",
            http_status=422,
        )

    seen: set[str] = set()
    cleaned: list[tuple[str, str]] = []
    for index, candidate in enumerate(candidates):
        if not isinstance(candidate, dict):
            raise OperationError(
                "invalid_payload",
                f"payload.candidates[{index}] must be an object",
                http_status=422,
            )
        _reject_unknown_keys(
            candidate, {"paper_id", "abstract"}, f"payload.candidates[{index}]"
        )
        paper_id = _require_str(
            candidate, "paper_id"
        )
        abstract = candidate.get("abstract", "")
        if not isinstance(abstract, str):
            raise OperationError(
                "invalid_payload",
                f"payload.candidates[{index}].abstract must be a string",
                http_status=422,
            )
        if paper_id in seen:
            raise OperationError(
                "duplicate_paper_id",
                f"paper_id {paper_id!r} appears more than once in candidates",
                http_status=422,
            )
        seen.add(paper_id)
        cleaned.append((paper_id, abstract))

    scores = []
    rendered_parts = []
    for paper_id, abstract in cleaned:
        # Deterministic pseudo-score from the id; mock ranking has no model.
        digest = hashlib.sha256(paper_id.encode("utf-8")).digest()
        score = round(digest[0] / 255.0, 4)
        reason = (
            f"mock score derived deterministically from paper_id {paper_id!r}; "
            "no semantic model involved"
        )
        scores.append({"paper_id": paper_id, "score": score, "reason": reason})
        rendered_parts.append(paper_id + abstract)

    usage = _mock_usage(question + "".join(a for _, a in cleaned), "".join(rendered_parts))
    return OperationResult(outputs={"scores": scores}, usage=usage)


def _op_fetch_full_text(payload: Mapping[str, Any]) -> OperationResult:
    _reject_unknown_keys(payload, {"paper_id", "doi", "oa_url", "size_limit"}, "payload")
    paper_id = _require_str(payload, "paper_id")

    oa_url = payload.get("oa_url")
    if oa_url is not None:
        if not isinstance(oa_url, str):
            raise OperationError(
                "invalid_payload", "payload.oa_url must be a string", http_status=422
            )
        # Defense in depth: the worker never reads local paths. Real download
        # constraints (DNS/IP checks, redirects, size) land with T04.
        if oa_url.startswith("file://") or oa_url.startswith("/"):
            raise OperationError(
                "invalid_oa_url",
                "payload.oa_url must be an http(s) URL, not a local path",
                http_status=422,
            )

    size_limit = payload.get("size_limit")
    if size_limit is not None:
        _require_int(payload, "size_limit", minimum=1, maximum=MAX_FETCH_SIZE_BYTES)

    doi = payload.get("doi")
    if doi is not None and not isinstance(doi, str):
        raise OperationError(
            "invalid_payload", "payload.doi must be a string", http_status=422
        )

    content = (
        f"[MOCK FULL TEXT for {paper_id}]\n"
        "This synthetic document exists to exercise the fetch_full_text contract. "
        "Paragraph one states a fabricated finding in generic terms.\n"
        "Paragraph two describes an invented methodology with no real-world "
        "counterpart. Downstream stages must treat this as mock data.\n"
    )
    encoded = content.encode("utf-8")
    size_limit_int = size_limit if isinstance(size_limit, int) else MAX_FETCH_SIZE_BYTES
    if len(encoded) > size_limit_int:
        raise OperationError(
            "content_too_large",
            "mock content exceeds payload.size_limit",
            http_status=422,
            retryable=False,
        )
    outputs = {
        "paper_id": paper_id,
        "content": content,
        "content_hash": _sha256(content),
        "media_type": "text/plain",
        "size_bytes": len(encoded),
        "acquisition_status": "acquired",
    }
    return OperationResult(
        outputs=outputs,
        usage=_mock_usage(paper_id, content),
        warnings=["fetch_full_text is a mock; blob is generated, not downloaded"],
    )


def _op_parse(payload: Mapping[str, Any]) -> OperationResult:
    _reject_unknown_keys(payload, {"document", "media_type", "parser_version"}, "payload")
    document = _require_str(payload, "document")

    media_type = payload.get("media_type")
    if media_type not in TEXT_MEDIA_TYPES:
        raise OperationError(
            "unsupported_media_type",
            f"payload.media_type must be one of {', '.join(TEXT_MEDIA_TYPES)}",
            http_status=422,
        )
    parser_version = payload.get("parser_version", "mock-parser-1")
    if not isinstance(parser_version, str) or not parser_version:
        raise OperationError(
            "invalid_payload", "payload.parser_version must be a non-empty string",
            http_status=422,
        )

    # Python string indices are codepoint offsets, matching the contract's
    # codepoint-offset requirement (T01).
    blocks: list[dict[str, Any]] = []
    cursor = 0
    for paragraph in document.split("\n\n"):
        stripped = paragraph.strip("\n")
        start = document.find(stripped, cursor) if stripped else cursor
        if stripped and start >= 0:
            cursor = start + len(stripped)
            blocks.append(
                {
                    "block_id": f"blk-{len(blocks) + 1:04d}",
                    "text": stripped,
                    "start_offset": start,
                    "end_offset": start + len(stripped),
                    "page": 1,
                    "block_hash": _sha256(stripped),
                }
            )
    if not blocks:
        blocks.append(
            {
                "block_id": "blk-0001",
                "text": "",
                "start_offset": 0,
                "end_offset": 0,
                "page": 1,
                "block_hash": _sha256(""),
            }
        )

    outputs = {
        "blocks": blocks,
        "coverage": {
            "media_type": media_type,
            "parser_version": parser_version,
            "total_blocks": len(blocks),
            "total_codepoints": len(document),
            "complete": True,
        },
    }
    warnings = []
    if len(blocks) == 1 and not blocks[0]["text"]:
        warnings.append("document produced no text blocks")
    return OperationResult(
        outputs=outputs,
        usage=_mock_usage(document, "".join(b["text"] for b in blocks)),
        warnings=warnings,
    )


def _op_read(payload: Mapping[str, Any]) -> OperationResult:
    _reject_unknown_keys(payload, {"research_question", "paper_id", "blocks", "reader_policy"}, "payload")
    question = _require_str(payload, "research_question")
    paper_id = _require_str(payload, "paper_id")

    blocks = payload.get("blocks")
    if not isinstance(blocks, list) or not blocks:
        raise OperationError(
            "invalid_payload",
            "payload.blocks must be a non-empty list",
            http_status=422,
        )
    if len(blocks) > MAX_READ_BLOCKS:
        raise OperationError(
            "invalid_payload",
            f"payload.blocks must hold at most {MAX_READ_BLOCKS} entries",
            http_status=422,
        )

    cleaned: list[dict[str, str]] = []
    for index, block in enumerate(blocks):
        if not isinstance(block, dict):
            raise OperationError(
                "invalid_payload",
                f"payload.blocks[{index}] must be an object",
                http_status=422,
            )
        _reject_unknown_keys(
            block, {"block_id", "text"}, f"payload.blocks[{index}]"
        )
        block_id = _require_str(block, "block_id")
        text = block.get("text", "")
        if not isinstance(text, str):
            raise OperationError(
                "invalid_payload",
                f"payload.blocks[{index}].text must be a string",
                http_status=422,
            )
        cleaned.append({"block_id": block_id, "text": text})

    policy = payload.get("reader_policy")
    if policy is not None and not isinstance(policy, dict):
        raise OperationError(
            "invalid_payload", "payload.reader_policy must be an object", http_status=422
        )

    claims = []
    evidence = []
    for i, block in enumerate(cleaned[:3]):
        claims.append(
            {
                "claim_id": f"claim-{i + 1:03d}",
                "statement": (
                    f"Mock claim {i + 1} derived from block {block['block_id']} "
                    f"for paper {paper_id}."
                ),
                "block_ids": [block["block_id"]],
                "kind": "source_assertion",
            }
        )
        quote = block["text"][:120]
        evidence.append(
            {
                "evidence_id": f"evd-{i + 1:03d}",
                "block_id": block["block_id"],
                "quote": quote,
                "locator": {
                    "page": 1,
                    "start_offset": 0,
                    "end_offset": len(block["text"]),
                },
            }
        )

    outputs = {
        "paper_id": paper_id,
        "claims": claims,
        "evidence": evidence,
        "limitations": [
            "mock reader: claims are synthetic and must not enter evidence packs",
            "read covers only the blocks supplied in this request",
        ],
        "blocks_read": [b["block_id"] for b in cleaned],
    }
    rendered = "".join(b["text"] for b in cleaned)
    usage = _mock_usage(question + paper_id + rendered, rendered)
    return OperationResult(
        outputs=outputs,
        usage=usage,
        warnings=["read is a mock; it performs no model inference"],
    )


#: Registry: operation name -> handler. The API layer only dispatches to
#: names in contracts.OPERATIONS; this table must stay in sync with it.
OPERATION_HANDLERS: dict[str, Callable[[Mapping[str, Any]], OperationResult]] = {
    "discover": _op_discover,
    "rank": _op_rank,
    "fetch_full_text": _op_fetch_full_text,
    "parse": _op_parse,
    "read": _op_read,
}


def run_operation(operation: str, payload: Mapping[str, Any]) -> OperationResult:
    """Dispatch a validated payload to its mock implementation."""
    handler = OPERATION_HANDLERS.get(operation)
    if handler is None:  # pragma: no cover - guarded by the API whitelist
        raise OperationError(
            "unknown_operation",
            f"operation {operation!r} is not in the whitelist",
            http_status=404,
        )
    return handler(payload)


def response_versions() -> dict[str, str]:
    return {
        "operation_version": SUPPORTED_OPERATION_VERSION,
        "worker_version": WORKER_VERSION,
    }
