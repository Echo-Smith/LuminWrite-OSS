"""Offline tests for the discovery providers and R03 dedup.

All HTTP interactions use injected ``httpx.MockTransport`` handlers; nothing
reaches the network. (The optional live check lives in test_live_smoke.py.)
"""

from __future__ import annotations

import json

import httpx
import pytest

from lumin_scholar import providers
from lumin_scholar.discovery import (
    check_allowlist,
    discover_papers,
    make_paper_id,
    merge_records,
)


def _client(handler) -> httpx.Client:
    return httpx.Client(transport=httpx.MockTransport(handler), timeout=5.0)


# ---------------------------------------------------------------------------
# Provider response parsing
# ---------------------------------------------------------------------------


class TestOpenAlexParsing:
    def test_records_normalised(self):
        def handler(request: httpx.Request) -> httpx.Response:
            assert "api.openalex.org/works" in str(request.url)
            assert request.url.params["search"] == "battery cathodes"
            body = {
                "results": [
                    {
                        "id": "https://openalex.org/W123",
                        "title": "Layered Cathode Stability",
                        "doi": "https://doi.org/10.1000/OA.1",
                        "publication_year": 2024,
                        "authorships": [
                            {"author": {"display_name": "María Curie"}},
                            {"author": None},  # malformed entry tolerated
                        ],
                        "primary_location": {"source": {"display_name": "J. of Batts"}},
                        "open_access": {"oa_url": "https://example.org/pdf"},
                        "abstract_inverted_index": {"Layered": [0], "cathodes": [1], "age": [2]},
                    }
                ]
            }
            return httpx.Response(200, json=body)

        records, status = providers.search_openalex(
            _client(handler), "battery cathodes", 5
        )
        assert status.status == "ok" and status.returned == 1
        record = records[0]
        assert record["title"] == "Layered Cathode Stability"
        assert record["doi"] == "10.1000/oa.1"  # normalised (lowercase)
        assert record["authors"] == ["María Curie"]
        assert record["year"] == 2024
        assert record["abstract"] == "Layered cathodes age"
        assert record["oa_url"] == "https://example.org/pdf"
        assert record["provider"] == "openalex"

    def test_malformed_fields_do_not_crash(self):
        body = {
            "results": [
                None,
                42,
                {
                    # everything missing / wrong type
                    "title": 7,
                    "authorships": "nope",
                    "publication_year": "twenty",
                    "primary_location": None,
                    "abstract_inverted_index": {"bad": ["x"], "worse": None},
                },
            ]
        }
        records, status = providers.search_openalex(
            _client(lambda req: httpx.Response(200, json=body)), "q", 5
        )
        assert status.status == "ok"
        assert len(records) == 1
        record = records[0]
        assert record["title"] is None
        assert record["authors"] == []
        assert record["year"] is None
        assert record["abstract"] is None  # unusable inverted index -> None

    def test_http_error_yields_error_status(self):
        records, status = providers.search_openalex(
            _client(lambda req: httpx.Response(500, text="boom")), "q", 5
        )
        assert records == []
        assert status.status == "error"
        assert status.error_code == "provider_http_error"

    def test_rate_limit_yields_rate_limited_code(self):
        _, status = providers.search_openalex(
            _client(lambda req: httpx.Response(429)), "q", 5
        )
        assert status.error_code == "provider_rate_limited"


class TestCrossrefParsing:
    def test_records_normalised(self):
        body = {
            "message": {
                "items": [
                    {
                        "DOI": "10.1000/CR.1",
                        "title": ["A Crossref Record <with> tags"],
                        "author": [
                            {"given": "Ada", "family": "Lovelace"},
                            {"name": "Analytical Engine Group"},
                            {"family": "NoGiven"},
                        ],
                        "issued": {"date-parts": [[2022, 3]]},
                        "container-title": ["J. Digit. Mater."],
                        "abstract": "<jats:p>Plain summary text.</jats:p>",
                        "resource": {"primary": {"URL": "https://example.org/paper"}},
                    }
                ]
            }
        }
        records, status = providers.search_crossref(
            _client(lambda req: httpx.Response(200, json=body)), "q", 5
        )
        assert status.status == "ok"
        record = records[0]
        assert record["doi"] == "10.1000/cr.1"
        assert record["authors"] == ["Lovelace, Ada", "Analytical Engine Group", "NoGiven"]
        assert record["year"] == 2022
        assert record["venue"] == "J. Digit. Mater."
        assert record["abstract"] == "Plain summary text."
        assert record["canonical_url"] == "https://example.org/paper"

    def test_bad_message_shape_yields_error(self):
        records, status = providers.search_crossref(
            _client(lambda req: httpx.Response(200, json={"message": "oops"})), "q", 5
        )
        assert records == []
        assert status.error_code == "provider_bad_json"


class TestSemanticScholarParsing:
    def test_records_normalised(self):
        body = {
            "data": [
                {
                    "paperId": "abc123",
                    "title": "S2 Record",
                    "year": 2023,
                    "authors": [{"name": "Grace Hopper"}],
                    "venue": "Conf",
                    "abstract": "An abstract.",
                    "externalIds": {"DOI": "10.1000/S2.1"},
                    "openAccessPdf": {"url": "https://example.org/s2.pdf"},
                    "url": "https://www.semanticscholar.org/paper/abc123",
                }
            ]
        }
        records, status = providers.search_semantic_scholar(
            _client(lambda req: httpx.Response(200, json=body)), "q", 5
        )
        assert status.status == "ok"
        record = records[0]
        assert record["doi"] == "10.1000/s2.1"
        assert record["provider_id"] == "DOI:10.1000/S2.1"
        assert record["oa_url"] == "https://example.org/s2.pdf"
        assert record["abstract"] == "An abstract."

    def test_missing_external_ids_keeps_paper_id(self):
        body = {"data": [{"paperId": "xyz", "title": None, "year": "bad"}]}
        records, _ = providers.search_semantic_scholar(
            _client(lambda req: httpx.Response(200, json=body)), "q", 5
        )
        assert records[0]["provider_id"] == "xyz"
        assert records[0]["title"] is None
        assert records[0]["year"] is None


# ---------------------------------------------------------------------------
# R03 dedup
# ---------------------------------------------------------------------------


def _rec(provider, title, authors, year, doi=None, provider_id=None):
    return {
        "title": title,
        "authors": authors,
        "year": year,
        "doi": doi,
        "venue": "V",
        "canonical_url": None,
        "abstract": None,
        "oa_url": None,
        "provider": provider,
        "provider_id": provider_id or f"{provider}-id",
    }


class TestMergeRecords:
    def test_same_doi_multiple_spellings_merge(self):
        # Two providers give the same DOI after normalisation.
        a = _rec("openalex", "Cathode Aging", ["M. Curie"], 2024, doi="10.1000/x.1")
        b = _rec("crossref", "Cathode aging", ["Curie"], 2024, doi="https://doi.org/10.1000/X.1")
        papers, _ = merge_records([a, b])
        assert len(papers) == 1
        assert papers[0]["doi"] == "10.1000/x.1"
        assert papers[0]["providers"] == ["openalex", "crossref"]
        assert papers[0]["aliases"] == ["openalex-id", "doi:10.1000/x.1", "crossref-id"]

    def test_same_title_conflicting_doi_kept_separate_with_hint(self):
        a = _rec("openalex", "Same Title Here", ["A. Author"], 2021, doi="10.1/aaa")
        b = _rec("crossref", "Same Title Here", ["B. Author"], 2021, doi="10.1/bbb")
        papers, warnings = merge_records([a, b])
        assert len(papers) == 2
        assert all(p["possible_duplicate"] for p in papers)
        assert warnings

    def test_missing_doi_same_title_author_year_merges(self):
        a = _rec("openalex", "No DOI Study", ["Zhang San"], 2020)
        b = _rec("semantic_scholar", "No DOI  study", ["San"], 2020)
        papers, _ = merge_records([a, b])
        assert len(papers) == 1
        assert papers[0]["providers"] == ["openalex", "semantic_scholar"]
        assert papers[0]["doi"] is None

    def test_missing_author_does_not_merge(self):
        a = _rec("openalex", "No DOI Study", ["Zhang San"], 2020)
        b = _rec("crossref", "No DOI Study", [], 2020)
        papers, _ = merge_records([a, b])
        assert len(papers) == 2
        assert not any(p["possible_duplicate"] for p in papers)

    def test_year_mismatch_does_not_merge(self):
        a = _rec("openalex", "No DOI Study", ["Zhang San"], 2020)
        b = _rec("crossref", "No DOI Study", ["Zhang San"], 2021)
        papers, _ = merge_records([a, b])
        assert len(papers) == 2

    def test_title_match_without_author_year_stays_separate(self):
        a = _rec("openalex", "Ambiguous Title", ["A. One"], None)
        b = _rec("crossref", "Ambiguous Title", ["B. Two"], None)
        papers, _ = merge_records([a, b])
        assert len(papers) == 2

    def test_paper_id_stable_for_doi_and_title(self):
        assert make_paper_id("x", "10.1/a", None) == make_paper_id("y", "10.1/A", None)
        id_by_title = make_paper_id("x", None, "Quantum Widgets")
        assert id_by_title == make_paper_id("y", None, "Quantum  widgets")
        assert id_by_title != make_paper_id("y", None, "Quantum Gadgets")


class TestDiscoverPapers:
    def test_allowlist_is_enforced(self):
        with pytest.raises(ValueError):
            check_allowlist(["openalex", "https://evil.internal"])

    def test_single_source_failure_does_not_affect_others(self):
        def handler(request: httpx.Request) -> httpx.Response:
            if "openalex" in str(request.url):
                return httpx.Response(500, text="down")
            if "crossref" in str(request.url):
                return httpx.Response(
                    200,
                    json={"message": {"items": [
                        {
                            "DOI": "10.1/cr",
                            "title": ["CR Work"],
                            "author": [{"family": "Doe"}],
                            "issued": {"date-parts": [[2024]]},
                        }
                    ]}},
                )
            return httpx.Response(200, json={"data": []})

        papers, statuses, warnings = discover_papers(
            "query", ["openalex", "crossref", "semantic_scholar"], 5, client=_client(handler)
        )
        assert len(statuses) == 3
        by_provider = {s["provider"]: s for s in statuses}
        assert by_provider["openalex"]["status"] == "error"
        assert by_provider["crossref"]["status"] == "ok"
        assert by_provider["semantic_scholar"]["status"] == "ok"
        assert len(papers) == 1 and papers[0]["doi"] == "10.1/cr"
        assert warnings  # openalex failure is surfaced as a warning

    def test_all_sources_fail_is_distinct_from_empty(self):
        def handler(request: httpx.Request) -> httpx.Response:
            return httpx.Response(503, text="unavailable")

        papers, statuses, _ = discover_papers(
            "q", ["openalex", "crossref"], 5, client=_client(handler)
        )
        assert papers == []
        assert all(s["status"] == "error" for s in statuses)

    def test_all_sources_succeed_but_zero_hits_is_empty(self):
        def handler(request: httpx.Request) -> httpx.Response:
            if "openalex" in str(request.url):
                return httpx.Response(200, json={"results": []})
            if "crossref" in str(request.url):
                return httpx.Response(200, json={"message": {"items": []}})
            return httpx.Response(200, json={"data": []})

        papers, statuses, _ = discover_papers(
            "q", ["openalex", "crossref", "semantic_scholar"], 5, client=_client(handler)
        )
        assert papers == []
        assert all(s["status"] == "ok" and s["returned"] == 0 for s in statuses)

    def test_request_parameters_match_public_api_conventions(self):
        seen: dict[str, httpx.URL] = {}

        def handler(request: httpx.Request) -> httpx.Response:
            seen[str(request.url.host)] = request.url
            if "openalex" in str(request.url):
                return httpx.Response(200, json={"results": []})
            if "crossref" in str(request.url):
                return httpx.Response(200, json={"message": {"items": []}})
            return httpx.Response(200, json={"data": []})

        discover_papers("lithium batteries", ["openalex", "crossref", "semantic_scholar"],
                        7, client=_client(handler))
        assert seen["api.openalex.org"].params["per-page"] == "7"
        assert seen["api.openalex.org"].params["search"] == "lithium batteries"
        assert seen["api.crossref.org"].params["rows"] == "7"
        assert "query.bibliographic" in str(seen["api.crossref.org"])
        assert seen["api.semanticscholar.org"].params["limit"] == "7"
        assert seen["api.semanticscholar.org"].params["fields"]
