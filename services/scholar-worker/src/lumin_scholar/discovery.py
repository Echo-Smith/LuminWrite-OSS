"""Discovery orchestration for the discover operation (T04).

Fan-out to a provider allowlist, then merge records into PaperCandidate rows
using the R03 dedup rule (contracts.md §2):

- DOI normalisation first (see :mod:`lumin_scholar.dois`).
- Cross-provider merge happens ONLY on exact DOI equality, or on
  (normalised title + first-author family name + year) all matching.
- Conflicting DOIs with similar titles stay separate records, each carrying a
  ``possible_duplicate`` hint. The upstream single-key "DOI or title" merge is
  deliberately NOT reproduced.
- Records with no DOI and no confirmed title/author/year match stay separate.

The provider allowlist is the security boundary: only providers listed in
``KNOWN_PROVIDERS`` are callable, so a caller cannot smuggle an arbitrary
base URL through the payload.
"""

from __future__ import annotations

import hashlib
import re
from typing import Any

import httpx

from . import providers
from .dois import normalize_doi

#: The payload allowlist may only name these providers; anything else is a
#: payload error (the worker must not learn new fetch targets at runtime).
DISCOVER_DEFAULT_LIMIT = 10


def check_allowlist(allowlist: list[str]) -> None:
    unknown = [p for p in allowlist if p not in providers.KNOWN_PROVIDERS]
    if unknown:
        raise ValueError(
            "unknown provider(s): " + ", ".join(sorted(unknown))
        )


def _family_name(author: str) -> str:
    """Best-effort family-name extraction for dedup keys (not display)."""
    name = author.strip()
    if not name:
        return ""
    if "," in name:
        return re.split(r"[,\s]+", name, maxsplit=1)[0].strip().lower()
    parts = name.split()
    return parts[-1].lower() if parts else ""


def _title_key(title: str) -> str:
    """Loose title normalisation for the author/year-assisted match."""
    return re.sub(r"[^a-z0-9\u4e00-\u9fff]+", "", title.lower())


def make_paper_id(provider_id: str, doi: str | None, title: str | None) -> str:
    """Stable paper_id: content-derived so the same work maps to the same id
    across runs when the merging keys agree.

    Format: ``p_<12 hex>`` — deterministic from the DOI when present, else
    from the normalised title. Not guessable-sequential and never a provider
    secret; provider originals are preserved in ``aliases``.
    """
    if doi:
        material = "doi:" + normalize_doi(doi)
    else:
        material = "title:" + _title_key(title or "")
    digest = hashlib.sha256(material.encode("utf-8")).hexdigest()
    return "p_" + digest[:12]


def merge_records(
    raw_records: list[dict[str, Any]],
) -> tuple[list[dict[str, Any]], list[str]]:
    """Merge raw provider records into PaperCandidate dicts (R03 rule).

    Returns ``(papers, warnings)``. Each paper carries:
    ``paper_id, doi, title, authors, year, venue, canonical_url, aliases,
    abstract, oa_url, providers, possible_duplicate``.
    """
    papers: list[dict[str, Any]] = []
    warnings: list[str] = []

    # Indexes for the two allowed merge paths.
    by_doi: dict[str, int] = {}
    by_title_author_year: dict[tuple[str, str, int], int] = {}

    for record in raw_records:
        provider = record.get("provider") or "unknown"
        provider_id = record.get("provider_id")
        title = record.get("title")
        authors: list[str] = list(record.get("authors") or [])
        year = record.get("year")
        # Defensive re-normalisation: adapters already normalise, but merge
        # correctness depends on the R03 key, so never trust upstream casing
        # or URL prefixes here.
        raw_doi = record.get("doi")
        doi = normalize_doi(raw_doi) if isinstance(raw_doi, str) and raw_doi else None
        abstract = record.get("abstract")
        oa_url = record.get("oa_url")

        aliases = [provider_id] if provider_id else []
        if doi:
            aliases.append(f"doi:{doi}")

        matched = -1
        if doi and doi in by_doi:
            matched = by_doi[doi]

        if matched < 0 and title and authors and isinstance(year, int):
            key = (_title_key(title), _family_name(authors[0]), year)
            matched = by_title_author_year.get(key, -1)

        if matched >= 0:
            paper = papers[matched]
            if paper["doi"] and doi and paper["doi"] != doi:
                # Different DOI with a title match: per R03 this is exactly
                # the ambiguous case — keep both records separate.
                matched = -1
            else:
                _merge_into(paper, provider, aliases, record)
        if matched < 0:
            paper = {
                "paper_id": make_paper_id(doi or "", doi, title),
                "doi": doi,
                "title": title,
                "authors": authors,
                "year": year,
                "venue": record.get("venue"),
                "canonical_url": record.get("canonical_url"),
                "aliases": aliases,
                "abstract": abstract,
                "oa_url": oa_url,
                "providers": [provider],
                "possible_duplicate": False,
            }
            papers.append(paper)
            index = len(papers) - 1
            if doi:
                by_doi[doi] = index
            if title and authors and isinstance(year, int):
                key = (_title_key(title), _family_name(authors[0]), year)
                by_title_author_year[key] = index

    # Same title arriving under two different DOIs -> mark both as possible
    # duplicates instead of merging (R03: conflicting DOIs are not merged).
    seen_titles: dict[str, int] = {}
    for index, paper in enumerate(papers):
        if not paper["title"]:
            continue
        tkey = _title_key(paper["title"])
        first = seen_titles.get(tkey)
        if first is None:
            seen_titles[tkey] = index
            continue
        if papers[first]["doi"] and paper["doi"] and papers[first]["doi"] != paper["doi"]:
            papers[first]["possible_duplicate"] = True
            paper["possible_duplicate"] = True
            warnings.append(
                f"possible duplicate papers kept separate (conflicting DOIs): "
                f"{papers[first]['paper_id']} vs {paper['paper_id']}"
            )

    return papers, warnings


def _merge_into(paper: dict[str, Any], provider: str, aliases: list[str], record: dict[str, Any]) -> None:
    """Fill one paper from another provider's record for the same work."""
    if provider not in paper["providers"]:
        paper["providers"].append(provider)
    for alias in aliases:
        if alias not in paper["aliases"]:
            paper["aliases"].append(alias)
    # Fill gaps only; first-seen values win for display fields so output is
    # deterministic for a given provider ordering.
    if not paper["abstract"] and record.get("abstract"):
        paper["abstract"] = record["abstract"]
    if not paper["venue"] and record.get("venue"):
        paper["venue"] = record.get("venue")
    if not paper["canonical_url"] and record.get("canonical_url"):
        paper["canonical_url"] = record.get("canonical_url")
    if not paper["oa_url"] and record.get("oa_url"):
        paper["oa_url"] = record.get("oa_url")
    if not paper["title"] and record.get("title"):
        paper["title"] = record.get("title")
    if paper["year"] is None and record.get("year") is not None:
        paper["year"] = record.get("year")
    if not paper["authors"] and record.get("authors"):
        paper["authors"] = list(record.get("authors") or [])


def discover_papers(
    query: str,
    allowlist: list[str],
    limit: int,
    *,
    client: httpx.Client | None = None,
    timeout_s: float = providers.PROVIDER_TIMEOUT_S,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]], list[str]]:
    """Run the provider fan-out and merge.

    Returns ``(papers, provider_status, warnings)``. All-provider failure is
    surfaced by an empty ``papers`` list with every status entry an error —
    the caller maps that to a distinct aggregate error code (never "no
    results", which is reserved for successful searches with zero hits).
    """
    check_allowlist(allowlist)
    own_client = client is None
    if own_client:
        client = providers._new_client()
    assert client is not None

    raw_records: list[dict[str, Any]] = []
    statuses: list[dict[str, Any]] = []
    merge_warnings: list[str] = []
    try:
        for provider in allowlist:
            records, status = providers.search_provider(
                provider, client, query, limit, timeout_s=timeout_s
            )
            statuses.append(status.to_dict())
            raw_records.extend(records)
    finally:
        if own_client:
            client.close()

    papers, merge_warnings = merge_records(raw_records)
    # Provider-level error notes. Single-source failures are surfaced both as
    # provider_results entries (status=error) and as warnings so the Go host
    # can log them without parsing status objects.
    warnings = list(merge_warnings)
    for status in statuses:
        if status.get("status") == "error" and status.get("error_code"):
            warnings.append(
                f"provider {status['provider']} error: {status['error_code']}"
            )

    return papers, statuses, warnings
