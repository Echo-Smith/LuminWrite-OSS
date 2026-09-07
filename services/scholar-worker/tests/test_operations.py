"""Unit tests for the five mock operations (pure functions, offline).

Locks down behaviors the Go client and later tasks (T04/T05) rely on:
- discover: limit clamping and synthetic canonical metadata
- rank: every input paper_id scored exactly once; missing abstract ok;
  too many candidates rejected
- fetch_full_text: content hash matches content; size_limit rejection;
  local-path oa_url rejected
- parse: codepoint offsets and per-block hashes are consistent
- read: evidence/claims reference the supplied blocks only
"""

from __future__ import annotations

import hashlib

import pytest

from lumin_scholar.operations import (
    MAX_FETCH_SIZE_BYTES,
    MAX_RANK_CANDIDATES,
    MAX_READ_BLOCKS,
    OperationError,
    run_operation,
)


def _sha256(text: str) -> str:
    return "sha256:" + hashlib.sha256(text.encode("utf-8")).hexdigest()


class TestDiscover:
    def test_returns_at_most_limit_records(self):
        result = run_operation("discover", {"query": "anything", "limit": 2})
        assert len(result.outputs["records"]) == 2
        record = result.outputs["records"][0]
        assert record["paper_id"] == "mock-paper-0001"
        assert record["doi"] == "10.0000/mock.0001"
        assert record["year"] == 2024

    def test_limit_above_cap_rejected(self):
        with pytest.raises(OperationError):
            run_operation("discover", {"query": "x", "limit": 51})

    def test_provider_not_in_allowlist_returns_empty(self):
        result = run_operation(
            "discover", {"query": "x", "provider_allowlist": ["arxiv"]}
        )
        assert result.outputs["records"] == []
        assert result.outputs["provider_status"][0]["status"] == "not_available"

    def test_unknown_payload_key_rejected(self):
        with pytest.raises(OperationError) as excinfo:
            run_operation("discover", {"query": "x", "api_key": "leak"})
        assert excinfo.value.code == "invalid_payload"


class TestRank:
    def test_scores_every_input_exactly_once(self):
        candidates = [
            {"paper_id": f"p{i}", "abstract": f"abstract {i}"} for i in range(5)
        ]
        result = run_operation(
            "rank", {"research_question": "why", "candidates": candidates}
        )
        scores = result.outputs["scores"]
        assert [s["paper_id"] for s in scores] == [f"p{i}" for i in range(5)]
        assert len({s["paper_id"] for s in scores}) == 5
        for s in scores:
            assert 0.0 <= s["score"] <= 1.0
            assert s["reason"]

    def test_missing_abstract_is_allowed(self):
        result = run_operation(
            "rank",
            {"research_question": "q", "candidates": [{"paper_id": "only"}]},
        )
        assert result.outputs["scores"][0]["paper_id"] == "only"

    def test_duplicate_id_raises(self):
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "rank",
                {
                    "research_question": "q",
                    "candidates": [
                        {"paper_id": "a", "abstract": ""},
                        {"paper_id": "a", "abstract": ""},
                    ],
                },
            )
        assert excinfo.value.code == "duplicate_paper_id"

    def test_too_many_candidates_rejected(self):
        candidates = [{"paper_id": f"p{i}", "abstract": ""} for i in range(MAX_RANK_CANDIDATES + 1)]
        with pytest.raises(OperationError) as excinfo:
            run_operation("rank", {"research_question": "q", "candidates": candidates})
        assert excinfo.value.code == "invalid_payload"


class TestFetchFullText:
    def test_content_hash_matches_content(self):
        result = run_operation("fetch_full_text", {"paper_id": "p1"})
        assert result.outputs["content_hash"] == _sha256(result.outputs["content"])
        assert result.outputs["acquisition_status"] == "acquired"
        assert result.outputs["media_type"] == "text/plain"

    def test_local_path_oa_url_rejected(self):
        for bad in ("file:///etc/passwd", "/etc/passwd"):
            with pytest.raises(OperationError) as excinfo:
                run_operation("fetch_full_text", {"paper_id": "p1", "oa_url": bad})
            assert excinfo.value.code == "invalid_oa_url"

    def test_size_limit_too_small_rejected(self):
        with pytest.raises(OperationError) as excinfo:
            run_operation("fetch_full_text", {"paper_id": "p1", "size_limit": 10})
        assert excinfo.value.code == "content_too_large"

    def test_size_limit_cap_enforced(self):
        with pytest.raises(OperationError):
            run_operation(
                "fetch_full_text", {"paper_id": "p1", "size_limit": MAX_FETCH_SIZE_BYTES + 1}
            )


class TestParse:
    def test_blocks_have_consistent_offsets_and_hashes(self):
        document = "First paragraph text.\n\nSecond paragraph with 中文."
        result = run_operation(
            "parse", {"document": document, "media_type": "text/plain"}
        )
        blocks = result.outputs["blocks"]
        assert len(blocks) == 2
        for block in blocks:
            segment = document[block["start_offset"]:block["end_offset"]]
            assert segment == block["text"]
            assert block["block_hash"] == _sha256(block["text"])
        assert result.outputs["coverage"]["total_codepoints"] == len(document)

    def test_markdown_media_type_supported(self):
        result = run_operation(
            "parse", {"document": "# Heading\n\nBody", "media_type": "text/markdown"}
        )
        assert result.outputs["coverage"]["media_type"] == "text/markdown"

    def test_unsupported_media_type_rejected(self):
        with pytest.raises(OperationError) as excinfo:
            run_operation("parse", {"document": "x", "media_type": "application/pdf"})
        assert excinfo.value.code == "unsupported_media_type"


class TestRead:
    def test_claims_and_evidence_reference_supplied_blocks(self):
        blocks = [
            {"block_id": "blk-1", "text": "alpha " * 40},
            {"block_id": "blk-2", "text": "beta"},
        ]
        result = run_operation(
            "read",
            {"research_question": "q", "paper_id": "p1", "blocks": blocks},
        )
        assert result.outputs["blocks_read"] == ["blk-1", "blk-2"]
        block_ids = {b["block_id"] for b in blocks}
        for claim in result.outputs["claims"]:
            assert set(claim["block_ids"]) <= block_ids
        for ev in result.outputs["evidence"]:
            assert ev["block_id"] in block_ids
            assert ev["quote"].startswith(("alpha", "beta"))
        assert result.outputs["limitations"]

    def test_too_many_blocks_rejected(self):
        blocks = [{"block_id": f"b{i}", "text": "x"} for i in range(MAX_READ_BLOCKS + 1)]
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "read", {"research_question": "q", "paper_id": "p", "blocks": blocks}
            )
        assert excinfo.value.code == "invalid_payload"

    def test_unknown_block_field_rejected(self):
        with pytest.raises(OperationError):
            run_operation(
                "read",
                {
                    "research_question": "q",
                    "paper_id": "p",
                    "blocks": [{"block_id": "b1", "text": "x", "page": 1}],
                },
            )


class TestRegistryParity:
    def test_registry_covers_whitelist(self):
        from lumin_scholar.contracts import OPERATIONS
        from lumin_scholar.operations import OPERATION_HANDLERS

        assert set(OPERATION_HANDLERS) == set(OPERATIONS)
