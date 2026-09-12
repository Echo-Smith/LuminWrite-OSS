"""Unit tests for the five operations (offline; T05 real implementations).

Locks down behaviors the Go client relies on:
- discover: provider allowlist subset validation, limit clamping, per-source
  status aggregation, all-sources-failed vs empty-result distinction
- rank: payload validation and fail-closed LLM config (successful LLM paths
  are covered in test_ranking.py with injected transports)
- fetch_full_text: URL/scheme validation, size cap, missing-OA-URL handling
  (network-side SSRF/redirect/size behaviour is in test_downloader.py)
- parse: bounded hash-verified blocks over base64 payloads (details in
  test_parser.py, incl. PDF fixtures)
- read: fail-closed without LLM config, payload shape validation (details in
  test_reader.py with injected transports)
"""

from __future__ import annotations

import base64
import hashlib

import httpx
import pytest

from lumin_scholar import parser
from lumin_scholar.operations import (
    MAX_DISCOVER_LIMIT,
    MAX_FETCH_SIZE_BYTES,
    MAX_RANK_CANDIDATES,
    MAX_READ_BLOCKS,
    OperationError,
    WorkerDeps,
    build_handlers,
    default_worker_deps,
    run_operation,
)


def _sha256(text: str) -> str:
    return "sha256:" + hashlib.sha256(text.encode("utf-8")).hexdigest()


def _mock_deps(handler) -> WorkerDeps:
    """Deps whose discovery/fetch clients use the given MockTransport handler;
    rank is fail-closed (no LLM configured)."""
    factory = lambda: httpx.Client(  # noqa: E731
        transport=httpx.MockTransport(handler), timeout=5.0, trust_env=False
    )
    return WorkerDeps(
        discover_client_factory=factory,
        download_client_factory=factory,
        llm_config_provider=lambda: None,
        llm_client_factory=None,
    )


def _openalex_ok(handler_body):
    def handler(request: httpx.Request) -> httpx.Response:
        if "openalex" in str(request.url):
            return httpx.Response(200, json={"results": handler_body})
        return httpx.Response(503, text="down")

    return handler


class TestDiscover:
    def test_returns_papers_and_provider_status(self):
        deps = _mock_deps(
            _openalex_ok(
                [
                    {
                        "id": "https://openalex.org/W1",
                        "title": "Mock Replacement Study",
                        "doi": "https://doi.org/10.0000/mock.0001",
                        "publication_year": 2024,
                        "authorships": [{"author": {"display_name": "A. Author"}}],
                    }
                ]
            )
        )
        result = run_operation(
            "discover",
            {"query": "anything", "limit": 2, "provider_allowlist": ["openalex"]},
            handlers=build_handlers(deps),
        )
        assert len(result.outputs["papers"]) == 1
        paper = result.outputs["papers"][0]
        assert paper["doi"] == "10.0000/mock.0001"
        assert paper["year"] == 2024
        assert paper["paper_id"].startswith("p_")
        assert result.outputs["provider_results"] == [
            {"provider": "openalex", "status": "ok", "returned": 1}
        ]

    def test_limit_above_cap_rejected(self):
        deps = _mock_deps(lambda request: httpx.Response(200, json={"results": []}))
        with pytest.raises(OperationError):
            run_operation(
                "discover", {"query": "x", "limit": MAX_DISCOVER_LIMIT + 1},
                handlers=build_handlers(deps),
            )

    def test_unknown_provider_rejected(self):
        deps = _mock_deps(lambda request: httpx.Response(200, json={"results": []}))
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "discover", {"query": "x", "provider_allowlist": ["arxiv"]},
                handlers=build_handlers(deps),
            )
        assert excinfo.value.code == "invalid_payload"

    def test_unknown_payload_key_rejected(self):
        deps = _mock_deps(lambda request: httpx.Response(200, json={"results": []}))
        with pytest.raises(OperationError) as excinfo:
            run_operation("discover", {"query": "x", "api_key": "leak"}, handlers=build_handlers(deps))
        assert excinfo.value.code == "invalid_payload"

    def test_all_sources_failed_is_aggregate_error_not_empty_result(self):
        deps = _mock_deps(lambda request: httpx.Response(503, text="down"))
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "discover",
                {"query": "x", "provider_allowlist": ["openalex", "crossref"]},
                handlers=build_handlers(deps),
            )
        assert excinfo.value.code == "all_providers_failed"
        assert excinfo.value.retryable is True
        assert excinfo.value.http_status == 502


class TestRank:
    def test_payload_validation(self):
        deps = _mock_deps(lambda request: httpx.Response(200))
        handlers = build_handlers(deps)
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "rank",
                {
                    "research_question": "q",
                    "candidates": [{"paper_id": "a", "abstract": ""}, {"paper_id": "a"}],
                },
                handlers=handlers,
            )
        assert excinfo.value.code == "duplicate_paper_id"

        candidates = [{"paper_id": f"p{i}", "abstract": ""} for i in range(MAX_RANK_CANDIDATES + 1)]
        with pytest.raises(OperationError):
            run_operation(
                "rank", {"research_question": "q", "candidates": candidates}, handlers=handlers
            )

    def test_unconfigured_llm_fails_closed_not_silent_zero(self):
        deps = _mock_deps(lambda request: httpx.Response(200))
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "rank",
                {
                    "research_question": "q",
                    "candidates": [{"paper_id": "p1", "abstract": "a"}],
                },
                handlers=build_handlers(deps),
            )
        assert excinfo.value.code == "llm_not_configured"
        assert excinfo.value.retryable is False


class TestFetchFullText:
    def test_missing_oa_url_fails_explicitly(self):
        deps = _mock_deps(lambda request: httpx.Response(200, text="x"))
        with pytest.raises(OperationError) as excinfo:
            run_operation("fetch_full_text", {"paper_id": "p1"}, handlers=build_handlers(deps))
        assert excinfo.value.code == "no_open_access_url"
        assert excinfo.value.retryable is False

    def test_local_path_oa_url_rejected(self):
        deps = _mock_deps(lambda request: httpx.Response(200, text="x"))
        for bad in ("file:///etc/passwd", "/etc/passwd"):
            with pytest.raises(OperationError) as excinfo:
                run_operation(
                    "fetch_full_text", {"paper_id": "p1", "oa_url": bad},
                    handlers=build_handlers(deps),
                )
            assert excinfo.value.code == "invalid_oa_url"

    def test_size_limit_cap_enforced(self):
        deps = _mock_deps(lambda request: httpx.Response(200, text="x"))
        with pytest.raises(OperationError):
            run_operation(
                "fetch_full_text",
                {"paper_id": "p1", "oa_url": "https://example.org/a.pdf",
                 "size_limit": MAX_FETCH_SIZE_BYTES + 1},
                handlers=build_handlers(deps),
            )

    def test_forbidden_target_reports_block(self):
        # 127.0.0.1 via a *production-style* deps bundle (default IP policy) —
        # the guard must reject before any bytes are read.
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "fetch_full_text",
                {"paper_id": "p1", "oa_url": "http://127.0.0.1:9/x.pdf"},
                handlers=build_handlers(
                    WorkerDeps(
                        discover_client_factory=lambda: httpx.Client(),
                        download_client_factory=lambda: httpx.Client(follow_redirects=False, trust_env=False),
                        llm_config_provider=lambda: None,
                    )
                ),
            )
        assert excinfo.value.code == "forbidden_target_ip"


class TestFetchFullTextBlocked:
    def test_loopback_target_blocked_in_default_wiring(self):
        # run_operation's default handlers == production wiring; the loopback
        # target must be refused by the SSRF guard, never connected to.
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "fetch_full_text",
                {"paper_id": "p1", "oa_url": "http://127.0.0.1:9/x.pdf"},
            )
        assert excinfo.value.code == "forbidden_target_ip"
        assert excinfo.value.retryable is False


class TestParse:
    def test_blocks_have_consistent_hashes(self):
        document = "First paragraph text.\n\nSecond paragraph with 中文."
        result = run_operation(
            "parse",
            {
                "document": base64.b64encode(document.encode("utf-8")).decode("ascii"),
                "media_type": "text/plain",
                "parser_version": parser.PARSER_VERSION,
            },
            handlers=build_handlers(default_worker_deps()),
        )
        blocks = result.outputs["blocks"]
        assert len(blocks) >= 2
        for block in blocks:
            assert block["block_hash"] == _sha256(block["text"])
            assert block["page"] is None
        assert result.outputs["coverage"]["total_codepoints"] == len(document)

    def test_markdown_media_type_supported(self):
        result = run_operation(
            "parse",
            {
                "document": base64.b64encode(b"# Heading\n\nBody").decode("ascii"),
                "media_type": "text/markdown",
                "parser_version": parser.PARSER_VERSION,
            },
            handlers=build_handlers(default_worker_deps()),
        )
        assert result.outputs["coverage"]["media_type"] == "text/markdown"

    def test_unsupported_media_type_rejected(self):
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "parse",
                {
                    "document": base64.b64encode(b"x").decode("ascii"),
                    "media_type": "application/json",
                    "parser_version": parser.PARSER_VERSION,
                },
                handlers=build_handlers(default_worker_deps()),
            )
        assert excinfo.value.code == "unsupported_media_type"


def _read_deps(llm_config) -> WorkerDeps:
    return WorkerDeps(
        discover_client_factory=lambda: httpx.Client(),
        download_client_factory=lambda: httpx.Client(trust_env=False),
        llm_config_provider=lambda: llm_config,
    )


class TestRead:
    def test_fail_closed_without_llm_config(self):
        blocks = [{"block_id": "blk-1", "text": "alpha", "block_hash": _sha256("alpha")}]
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "read",
                {
                    "research_question": "q",
                    "paper_id": "p1",
                    "blocks": blocks,
                    "reader_policy": {"reader_policy_version": "reader/1"},
                },
                handlers=build_handlers(_read_deps(None)),
            )
        assert excinfo.value.code == "llm_not_configured"

    def test_too_many_blocks_rejected(self):
        blocks = [{"block_id": f"b{i}", "text": "x", "block_hash": _sha256("x")} for i in range(MAX_READ_BLOCKS + 1)]
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "read",
                {
                    "research_question": "q",
                    "paper_id": "p",
                    "blocks": blocks,
                    "reader_policy": {"reader_policy_version": "reader/1"},
                },
                handlers=build_handlers(_read_deps(None)),
            )
        assert excinfo.value.code == "invalid_payload"

    def test_block_hash_mismatch_rejected(self):
        blocks = [{"block_id": "b1", "text": "x", "block_hash": _sha256("other")}]
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "read",
                {
                    "research_question": "q",
                    "paper_id": "p",
                    "blocks": blocks,
                    "reader_policy": {"reader_policy_version": "reader/1"},
                },
                handlers=build_handlers(_read_deps(None)),
            )
        assert excinfo.value.code == "block_hash_mismatch"

    def test_unknown_block_field_rejected(self):
        blocks = [{"block_id": "b1", "text": "x", "block_hash": _sha256("x"), "page": 1}]
        with pytest.raises(OperationError):
            run_operation(
                "read",
                {
                    "research_question": "q",
                    "paper_id": "p",
                    "blocks": blocks,
                    "reader_policy": {"reader_policy_version": "reader/1"},
                },
                handlers=build_handlers(_read_deps(None)),
            )


class TestRegistryParity:
    def test_registry_covers_whitelist(self):
        from lumin_scholar.contracts import OPERATIONS
        from lumin_scholar.operations import build_handlers, default_worker_deps

        assert set(build_handlers(default_worker_deps())) == set(OPERATIONS)
