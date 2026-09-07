"""OPTIONAL live smoke test — real network to api.openalex.org.

Skipped by default (CI must stay deterministic and offline). Enable with:

    SCHOLAR_LIVE_TESTS=1 .venv/bin/python -m pytest tests/test_live_smoke.py -v

It validates reachability and the *shape* of the real response only — no
result-count assertions (network content shifts), no credentials involved.
"""

from __future__ import annotations

import os

import pytest

from lumin_scholar import providers

pytestmark = pytest.mark.skipif(
    os.environ.get("SCHOLAR_LIVE_TESTS") != "1",
    reason="live smoke disabled unless SCHOLAR_LIVE_TESTS=1 (real network)",
)


def test_openalex_live_query_shape():
    client = providers._new_client()
    try:
        records, status = providers.search_openalex(client, "lithium battery degradation", 3)
    finally:
        client.close()
    assert status.status == "ok", f"live OpenAlex query failed: {status.error_code}"
    assert len(records) >= 1
    record = records[0]
    # Shape assertions only: keys exist with the documented types/None.
    assert set(record) >= {
        "title", "authors", "year", "doi", "venue", "canonical_url",
        "abstract", "oa_url", "provider", "provider_id",
    }
    assert record["provider"] == "openalex"
    assert record["title"] is None or isinstance(record["title"], str)
    assert record["year"] is None or isinstance(record["year"], int)
    assert record["authors"] is None or isinstance(record["authors"], list)


def test_discover_live_end_to_end_shape():
    from lumin_scholar.discovery import discover_papers

    client = providers._new_client()
    try:
        papers, statuses, _warnings = discover_papers(
            "solid electrolyte interface", ["openalex"], 5, client=client
        )
    finally:
        client.close()
    assert all(s["status"] == "ok" for s in statuses), statuses
    for paper in papers:
        assert paper["paper_id"].startswith("p_")
        assert paper["providers"]
        assert paper["aliases"]
        assert "possible_duplicate" in paper
