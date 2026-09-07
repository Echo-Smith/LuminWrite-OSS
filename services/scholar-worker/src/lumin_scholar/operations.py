"""Operation registry and implementations for the Scholar Worker.

Each operation is a bounded unit: a pure function of its ``payload`` plus the
injected dependencies, returning ``OperationResult(outputs, usage, warnings)``.

T05 state of the five operations:

- ``discover``        — REAL: provider fan-out (OpenAlex/Crossref/Semantic
  Scholar) with per-source isolation and R03 dedup (see ``discovery.py``).
- ``rank``            — REAL: OpenAI-compatible LLM scoring, fail-closed when
  the worker env lacks ``SCHOLAR_LLM_*`` (see ``ranking.py``).
- ``fetch_full_text`` — REAL: constrained downloader with SSRF guards
  (see ``downloader.py``).
- ``parse``           — REAL: pypdf-backed PDF + native TXT/MD bounding into
  hash-verified blocks (see ``parser.py``).
- ``read``            — REAL: LLM-backed reader with worker-side evidence
  self-validation, fail-closed without ``SCHOLAR_LLM_*`` (see ``reader.py``).

Dependency injection: handlers close over a :class:`WorkerDeps` bundle that
supplies HTTP client factories and the LLM configuration. Production wiring
(``default_worker_deps``) builds real network clients and reads ``SCHOLAR_*``
env vars; tests construct :class:`WorkerDeps` with injected
``httpx.MockTransport`` clients and never touch the network. Note for
reviewers: the production paths pass NO bypass into the downloader — the
default ``downloader.build_default_ip_policy()`` applies and there is no
knob to weaken it.

No code, prompts, or fixtures are copied from the upstream AutoResearch
references (Proprietary); all implementations here are original.
"""

from __future__ import annotations

import base64
import binascii
from dataclasses import dataclass, field
from typing import Any, Callable, Mapping

import httpx

from . import downloader, discovery, parser, providers, ranking, reader
from .contracts import Usage
from . import SUPPORTED_OPERATION_VERSION, __version__

WORKER_VERSION = __version__

MAX_RANK_CANDIDATES = ranking.MAX_RANK_CANDIDATES  # 8, contracts.md §4
MAX_READ_BLOCKS = reader.MAX_READ_BLOCKS  # 24, contracts.md §4
MAX_DISCOVER_LIMIT = 50
MAX_FETCH_SIZE_BYTES = downloader.DEFAULT_SIZE_LIMIT  # 25 MiB design cap

TEXT_MEDIA_TYPES = ("text/plain", "text/markdown")

DISCOVER_PROVIDERS: tuple[str, ...] = (
    providers.PROVIDER_OPENALEX,
    providers.PROVIDER_CROSSREF,
    providers.PROVIDER_SEMANTIC_SCHOLAR,
)


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


@dataclass(frozen=True)
class WorkerDeps:
    """Injected dependencies for the real operations.

    ``*_client_factory`` return fresh :class:`httpx.Client` instances per call
    (closing is the handler's job). ``llm_config_provider`` returns the LLM
    config or None; None makes ``rank`` fail closed.
    """

    discover_client_factory: Callable[[], httpx.Client]
    download_client_factory: Callable[[], httpx.Client]
    llm_config_provider: Callable[[], ranking.LLMConfig | None]
    llm_client_factory: Callable[[], httpx.Client] | None = None


def default_worker_deps() -> WorkerDeps:
    """Production wiring: real network clients, env-provided LLM config.

    Reviewer note: no SSRF bypass is passed anywhere here — the downloader
    always uses ``build_default_ip_policy()``.
    """
    return WorkerDeps(
        discover_client_factory=providers._new_client,
        download_client_factory=downloader.make_download_client,
        llm_config_provider=ranking.LLMConfig.from_env,
        llm_client_factory=None,
    )


def _non_llm_usage(provider_label: str) -> Usage:
    """Usage for operations that perform no model call (T04)."""
    return Usage(
        measured=False,
        input_tokens=0,
        output_tokens=0,
        cost_usd=None,
        provider=provider_label,
        model="none",
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
# discover — real provider fan-out
# ---------------------------------------------------------------------------


def _op_discover(payload: Mapping[str, Any], deps: WorkerDeps) -> OperationResult:
    _reject_unknown_keys(payload, {"query", "provider_allowlist", "limit"}, "payload")
    query = _require_str(payload, "query")

    allowlist = payload.get("provider_allowlist", list(DISCOVER_PROVIDERS))
    if not isinstance(allowlist, list) or not allowlist or not all(
        isinstance(item, str) and item for item in allowlist
    ):
        raise OperationError(
            "invalid_payload",
            "payload.provider_allowlist must be a non-empty list of non-empty strings",
            http_status=422,
        )
    try:
        discovery.check_allowlist(allowlist)
    except ValueError as exc:
        raise OperationError("invalid_payload", str(exc), http_status=422) from exc

    limit = payload.get("limit", discovery.DISCOVER_DEFAULT_LIMIT)
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

    client = deps.discover_client_factory()
    try:
        papers, statuses, warnings = discovery.discover_papers(
            query, allowlist, limit, client=client
        )
    finally:
        client.close()

    error_statuses = [s for s in statuses if s.get("status") == "error"]
    if statuses and len(error_statuses) == len(statuses):
        codes = ",".join(sorted({str(s.get("error_code")) for s in error_statuses}))
        raise OperationError(
            "all_providers_failed",
            f"every provider failed ({codes}); this is an upstream error, "
            "not an empty result",
            http_status=502,
            retryable=True,
        )

    warnings.extend(f"provider {s['provider']} error: {s['error_code']}" for s in error_statuses)
    usage = _non_llm_usage(
        allowlist[0] if len(allowlist) == 1 else "multi"
    )
    return OperationResult(
        outputs={
            "papers": papers,
            "provider_results": statuses,
        },
        usage=usage,
        warnings=warnings,
    )


# ---------------------------------------------------------------------------
# rank — real LLM scoring (fail-closed)
# ---------------------------------------------------------------------------


def _op_rank(payload: Mapping[str, Any], deps: WorkerDeps) -> OperationResult:
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
    cleaned: list[dict[str, str]] = []
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
        paper_id = _require_str(candidate, "paper_id")
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
        cleaned.append({"paper_id": paper_id, "abstract": abstract})

    llm_config = deps.llm_config_provider()
    llm_client = deps.llm_client_factory() if deps.llm_client_factory is not None else None
    try:
        scores, usage = ranking.rank_candidates(
            question,
            cleaned,
            config=llm_config,
            client=llm_client,
        )
    except ranking.RankError as exc:
        raise OperationError(
            exc.code,
            exc.message,
            http_status=422,
            retryable=exc.retryable,
        ) from exc
    finally:
        if llm_client is not None:
            llm_client.close()

    return OperationResult(outputs={"scores": scores}, usage=usage)


# ---------------------------------------------------------------------------
# fetch_full_text — real constrained downloader
# ---------------------------------------------------------------------------

#: Evolution point (contracts.md §4 blob transport): T04 delivers the blob
#: INLINE as base64 inside the response (``transport="inline_base64"``) — the
#: worker never writes local files and never accepts local paths. When the Go
#: content service lands (T05/T09) this switches to a task-scoped short-lived
#: upload URL (``transport="content_service_url"``, ``blob_url`` non-null)
#: after Go verifies size/hash and stores the Artifact. Go remains the only
#: component that commits artifacts.
BLOB_TRANSPORT_INLINE = "inline_base64"
BLOB_TRANSPORT_CONTENT_SERVICE = "content_service_url"


def _op_fetch_full_text(payload: Mapping[str, Any], deps: WorkerDeps) -> OperationResult:
    _reject_unknown_keys(payload, {"paper_id", "doi", "oa_url", "size_limit"}, "payload")
    paper_id = _require_str(payload, "paper_id")

    oa_url = payload.get("oa_url")
    if oa_url is not None and not isinstance(oa_url, str):
        raise OperationError(
            "invalid_payload", "payload.oa_url must be a string", http_status=422
        )
    doi = payload.get("doi")
    if doi is not None and not isinstance(doi, str):
        raise OperationError(
            "invalid_payload", "payload.doi must be a string", http_status=422
        )

    size_limit = payload.get("size_limit")
    if size_limit is not None:
        _require_int(payload, "size_limit", minimum=1, maximum=MAX_FETCH_SIZE_BYTES)
        size_limit = int(size_limit)
    else:
        size_limit = MAX_FETCH_SIZE_BYTES

    if not oa_url:
        # No OA location is known for this paper. Resolving OA from a DOI
        # (Unpaywall-style) is future scope; this unit fails explicitly
        # instead of pretending to have content.
        raise OperationError(
            "no_open_access_url",
            f"no OA URL supplied for paper {paper_id!r}; acquisition cannot proceed",
            http_status=422,
        )

    client = deps.download_client_factory()
    try:
        result = downloader.constrained_download(client, oa_url, size_limit=size_limit)
    except downloader.DownloadError as exc:
        raise OperationError(
            exc.code,
            f"{exc.detail}",
            http_status=502 if exc.code in {
                "download_http_error", "download_unreachable", "download_timeout",
            } else 422,
            retryable=exc.code in {
                "download_http_error", "download_unreachable", "download_timeout",
            },
        ) from exc
    finally:
        client.close()

    usage = _non_llm_usage("oa_downloader")
    return OperationResult(
        outputs={
            "paper_id": paper_id,
            "acquisition_status": "full_text_available",
            "content_hash": result.content_hash,
            "size_bytes": result.size_bytes,
            "media_type": result.media_type,
            "content_type_reported": result.content_type_reported,
            "looks_like_pdf": result.looks_like_pdf,
            "likely_scanned": result.likely_scanned,
            "final_url": result.final_url,
            "redirect_hops": result.redirect_hops,
            "content_base64": base64.b64encode(result.content).decode("ascii"),
            "blob": {
                "transport": BLOB_TRANSPORT_INLINE,
                "blob_url": None,  # T05/T09: Go content-service upload URL
            },
        },
        usage=usage,
        warnings=[],
    )


# ---------------------------------------------------------------------------
# parse — real pypdf / plain-text parsing
# ---------------------------------------------------------------------------


def _op_parse(payload: Mapping[str, Any], deps: WorkerDeps) -> OperationResult:
    _reject_unknown_keys(payload, {"document", "media_type", "parser_version"}, "payload")

    media_type = payload.get("media_type")
    if media_type not in parser.PDF_MEDIA_TYPE and media_type not in parser.TEXT_MEDIA_TYPES:
        raise OperationError(
            "unsupported_media_type",
            "payload.media_type must be one of "
            f"{', '.join((parser.PDF_MEDIA_TYPE, *parser.TEXT_MEDIA_TYPES))}",
            http_status=422,
        )

    parser_version = payload.get("parser_version")
    if not isinstance(parser_version, str) or not parser_version.strip():
        raise OperationError(
            "invalid_payload",
            "payload.parser_version must be a non-empty string",
            http_status=422,
        )
    if parser_version != parser.PARSER_VERSION:
        # Fail closed on version skew: the host's ledger input_hash and the
        # pack's parsed-document refs are pinned to the parser version.
        raise OperationError(
            "unsupported_parser_version",
            f"parser_version {parser_version!r} not supported "
            f"(supported: {parser.PARSER_VERSION})",
            http_status=422,
        )

    document_b64 = payload.get("document")
    if not isinstance(document_b64, str) or not document_b64:
        raise OperationError(
            "invalid_payload",
            "payload.document must be a non-empty base64 string",
            http_status=422,
        )
    try:
        data = base64.b64decode(document_b64, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise OperationError(
            "invalid_document_encoding",
            f"payload.document is not valid base64: {exc}",
            http_status=422,
        ) from exc

    try:
        outputs = parser.parse_document(data, str(media_type))
    except parser.ParseError as exc:
        raise OperationError(exc.code, exc.message, http_status=422, retryable=exc.retryable) from exc

    return OperationResult(
        outputs={"blocks": outputs["blocks"], "coverage": outputs["coverage"]},
        usage=_non_llm_usage("parser"),
        warnings=outputs["warnings"],
    )


# ---------------------------------------------------------------------------
# read — real LLM reading with worker-side evidence validation
# ---------------------------------------------------------------------------


def _op_read(payload: Mapping[str, Any], deps: WorkerDeps) -> OperationResult:
    _reject_unknown_keys(
        payload, {"research_question", "paper_id", "blocks", "reader_policy"}, "payload"
    )
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

    cleaned: list[dict[str, Any]] = []
    for index, block in enumerate(blocks):
        if not isinstance(block, dict):
            raise OperationError(
                "invalid_payload",
                f"payload.blocks[{index}] must be an object",
                http_status=422,
            )
        _reject_unknown_keys(
            block, {"block_id", "text", "block_hash", "page"}, f"payload.blocks[{index}]"
        )
        block_id = _require_str(block, "block_id")
        text = block.get("text")
        if not isinstance(text, str) or not text:
            raise OperationError(
                "invalid_payload",
                f"payload.blocks[{index}].text must be a non-empty string",
                http_status=422,
            )
        block_hash = block.get("block_hash")
        if not isinstance(block_hash, str) or not block_hash:
            raise OperationError(
                "invalid_payload",
                f"payload.blocks[{index}].block_hash must be a non-empty string",
                http_status=422,
            )
        if block_hash != reader.sha256_text(text):
            raise OperationError(
                "block_hash_mismatch",
                f"payload.blocks[{index}].block_hash does not match its text",
                http_status=422,
            )
        page = block.get("page")
        if page is not None and (isinstance(page, bool) or not isinstance(page, int) or page < 1):
            raise OperationError(
                "invalid_payload",
                f"payload.blocks[{index}].page must be a positive integer or null",
                http_status=422,
            )
        cleaned.append({"block_id": block_id, "text": text, "block_hash": block_hash, "page": page})

    policy = payload.get("reader_policy")
    if policy is None or not isinstance(policy, dict):
        raise OperationError(
            "invalid_payload", "payload.reader_policy must be an object", http_status=422
        )
    _reject_unknown_keys(policy, {"reader_policy_version"}, "payload.reader_policy")
    policy_version = _require_str(policy, "reader_policy_version")

    llm_config = deps.llm_config_provider()
    llm_client = deps.llm_client_factory() if deps.llm_client_factory is not None else None
    try:
        outputs, usage, warnings = reader.read_paper(
            question,
            paper_id,
            cleaned,
            policy_version,
            config=llm_config,
            client=llm_client,
        )
    except reader.ReaderError as exc:
        raise OperationError(
            exc.code,
            exc.message,
            http_status=422,
            retryable=exc.retryable,
        ) from exc
    finally:
        if llm_client is not None:
            llm_client.close()

    return OperationResult(outputs=outputs, usage=usage, warnings=warnings)


# ---------------------------------------------------------------------------
# Registry
# ---------------------------------------------------------------------------

#: Handler signature: (payload, deps) -> OperationResult. The mock-only
#: handlers ignore deps but keep the same signature for uniformity.
Handler = Callable[[Mapping[str, Any], WorkerDeps], OperationResult]


def build_handlers(deps: WorkerDeps) -> dict[str, Handler]:
    """Build the operation registry over the given dependencies."""
    return {
        "discover": lambda payload, d=deps: _op_discover(payload, d),
        "rank": lambda payload, d=deps: _op_rank(payload, d),
        "fetch_full_text": lambda payload, d=deps: _op_fetch_full_text(payload, d),
        "parse": lambda payload, d=deps: _op_parse(payload, d),
        "read": lambda payload, d=deps: _op_read(payload, d),
    }


_DEFAULT_HANDLERS: dict[str, Handler] | None = None


def _default_handlers() -> dict[str, Handler]:
    global _DEFAULT_HANDLERS
    if _DEFAULT_HANDLERS is None:
        _DEFAULT_HANDLERS = build_handlers(default_worker_deps())
    return _DEFAULT_HANDLERS


def run_operation(
    operation: str,
    payload: Mapping[str, Any],
    *,
    handlers: dict[str, Handler] | None = None,
) -> OperationResult:
    """Dispatch a validated payload to its implementation.

    ``handlers`` overrides the default registry (tests inject fakes there).
    """
    registry = handlers if handlers is not None else _default_handlers()
    handler = registry.get(operation)
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
