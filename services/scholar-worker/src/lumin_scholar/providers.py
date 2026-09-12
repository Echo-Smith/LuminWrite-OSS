"""Provider adapters for real scholarly discovery (T04).

One adapter per public API (OpenAlex, Crossref, Semantic Scholar). All
adapters share the same small interface so :mod:`lumin_scholar.discovery` can
fan out to a provider allowlist without caring about wire details:

- Each adapter owns its base URL, request shaping and response extraction, and
  returns ``(records, status)``: records in the raw normalised-item shape plus
  a :class:`ProviderStatus` entry. A provider failing (timeout, HTTP error,
  malformed body) yields ``status="error"`` with an ``error_code`` and never
  raises past the adapter boundary — one broken source must not sink the rest.
- Every adapter takes an explicit ``httpx.Client`` plus per-call timeout, so
  tests inject ``httpx.MockTransport`` and stay fully offline.
- Request parameters follow each API's public documentation conventions
  (``search``/``filter``/``select`` for OpenAlex, ``query.bibliographic`` for
  Crossref, ``query``/``fields`` for Semantic Scholar). No API key is used or
  accepted for the T04 sources; Semantic Scholar is called unauthenticated on
  the public endpoint.

Adapters are original work for LuminBuddy: nothing here copies the upstream
AutoResearch reference implementation (which is Proprietary).
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import httpx

from .dois import normalize_doi

#: Per-provider HTTP defaults (all external calls have timeouts; design.md §9).
PROVIDER_TIMEOUT_S = 15.0
PROVIDER_USER_AGENT = (
    "lumin-scholar-worker/0.1 (+https://luminbuddy.example; research discovery)"
)

#: Known provider ids (contracts.md §4 discover provider allowlist).
PROVIDER_OPENALEX = "openalex"
PROVIDER_CROSSREF = "crossref"
PROVIDER_SEMANTIC_SCHOLAR = "semantic_scholar"
KNOWN_PROVIDERS: frozenset[str] = frozenset(
    {PROVIDER_OPENALEX, PROVIDER_CROSSREF, PROVIDER_SEMANTIC_SCHOLAR}
)

OPENALEX_BASE_URL = "https://api.openalex.org/works"
CROSSREF_BASE_URL = "https://api.crossref.org/works"
SEMANTIC_SCHOLAR_BASE_URL = "https://api.semanticscholar.org/graph/v1/paper/search"


@dataclass(frozen=True)
class ProviderStatus:
    """Per-source result entry (status ok/error; single-source isolation)."""

    provider: str
    status: str  # "ok" | "error"
    returned: int
    error_code: str | None = None

    def to_dict(self) -> dict[str, Any]:
        body: dict[str, Any] = {
            "provider": self.provider,
            "status": self.status,
            "returned": self.returned,
        }
        if self.error_code is not None:
            body["error_code"] = self.error_code
        return body


class _ProviderHTTPError(Exception):
    """Internal adapter failure; converted to a ProviderStatus error entry."""

    def __init__(self, code: str, detail: str) -> None:
        super().__init__(detail)
        self.code = code


def _int_field(value: Any) -> int | None:
    """Best-effort int extraction; provider quirks (bool, str digits) tolerated."""
    if isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value
    if isinstance(value, str) and value.strip().isdigit():
        return int(value.strip())
    return None


def _str_field(value: Any) -> str | None:
    if isinstance(value, str):
        stripped = value.strip()
        return stripped or None
    return None


def _new_client() -> httpx.Client:
    """Production client: pooled, real network, bounded headers.

    Tests never use this; they inject a client with an ``httpx.MockTransport``.
    """
    return httpx.Client(
        headers={"User-Agent": PROVIDER_USER_AGENT, "Accept": "application/json"},
        follow_redirects=False,
        timeout=PROVIDER_TIMEOUT_S,
    )


def _fetch_json(client: httpx.Client, url: str, params: dict[str, Any]) -> dict[str, Any]:
    """GET one JSON document with strict expectations (non-2xx / non-JSON fail)."""
    try:
        response = client.get(url, params=params)
    except httpx.TimeoutException as exc:
        raise _ProviderHTTPError("provider_timeout", f"{url}: timed out: {exc}") from exc
    except httpx.TransportError as exc:
        raise _ProviderHTTPError("provider_unreachable", f"{url}: {exc}") from exc
    if response.status_code == 429:
        raise _ProviderHTTPError("provider_rate_limited", f"{url}: 429 too many requests")
    if response.status_code >= 400:
        raise _ProviderHTTPError(
            "provider_http_error", f"{url}: HTTP {response.status_code}"
        )
    if response.status_code >= 300:
        raise _ProviderHTTPError(
            "provider_redirect_unexpected", f"{url}: unexpected redirect status"
        )
    try:
        decoded = response.json()
    except ValueError as exc:
        raise _ProviderHTTPError("provider_bad_json", f"{url}: non-JSON body") from exc
    if not isinstance(decoded, dict):
        raise _ProviderHTTPError("provider_bad_json", f"{url}: body is not an object")
    return decoded


# ---------------------------------------------------------------------------
# OpenAlex
# ---------------------------------------------------------------------------


def _openalex_author_names(item: dict[str, Any]) -> list[str]:
    authorships = item.get("authorships")
    if not isinstance(authorships, list):
        return []
    names: list[str] = []
    for entry in authorships:
        if not isinstance(entry, dict):
            continue
        author = entry.get("author")
        name = author.get("display_name") if isinstance(author, dict) else None
        if isinstance(name, str) and name.strip():
            names.append(name.strip())
    return names


def _openalex_oa_url(item: dict[str, Any]) -> str | None:
    oa = item.get("open_access")
    if isinstance(oa, dict):
        return _str_field(oa.get("oa_url"))
    return None


def parse_openalex_item(item: Any) -> dict[str, Any] | None:
    """Extract one normalised record from an OpenAlex ``works`` entry.

    Robust to malformed fields: a missing title/authors/year becomes None/[]
    rather than a crash; entries that are not objects are skipped.
    """
    if not isinstance(item, dict):
        return None
    title = _str_field(item.get("title")) or _str_field(item.get("display_name"))
    doi_raw = _str_field(item.get("doi"))
    year = _int_field(item.get("publication_year"))
    venue: str | None = None
    primary = item.get("primary_location")
    if isinstance(primary, dict):
        source = primary.get("source")
        if isinstance(source, dict):
            venue = _str_field(source.get("display_name"))
    return {
        "title": title,
        "authors": _openalex_author_names(item),
        "year": year,
        "doi": normalize_doi(doi_raw) if doi_raw else None,
        "venue": venue,
        "canonical_url": _str_field(item.get("id")),
        "abstract": _openalex_abstract(item),
        "oa_url": _openalex_oa_url(item),
        "provider": PROVIDER_OPENALEX,
        "provider_id": _str_field(item.get("id")),
    }


def _openalex_abstract(item: dict[str, Any]) -> str | None:
    """Rebuild the abstract from OpenAlex's inverted index, if present."""
    index = item.get("abstract_inverted_index")
    if not isinstance(index, dict) or not index:
        return _str_field(item.get("abstract"))
    positions: list[tuple[int, str]] = []
    for word, spots in index.items():
        if not isinstance(word, str) or not isinstance(spots, list):
            continue
        for spot in spots:
            if isinstance(spot, int) and spot >= 0:
                positions.append((spot, word))
    if not positions:
        return None
    positions.sort()
    return " ".join(word for _, word in positions)


def search_openalex(
    client: httpx.Client,
    query: str,
    limit: int,
    *,
    base_url: str = OPENALEX_BASE_URL,
    timeout_s: float = PROVIDER_TIMEOUT_S,
) -> tuple[list[dict[str, Any]], ProviderStatus]:
    """Query OpenAlex ``works``; returns (records, status) and never raises."""
    try:
        body = _fetch_json(
            client,
            base_url,
            {
                "search": query,
                "per-page": str(limit),
                "select": ",".join(
                    [
                        "id",
                        "title",
                        "display_name",
                        "doi",
                        "publication_year",
                        "authorships",
                        "primary_location",
                        "open_access",
                        "abstract_inverted_index",
                    ]
                ),
            },
        )
    except _ProviderHTTPError as exc:
        return [], ProviderStatus(PROVIDER_OPENALEX, "error", 0, exc.code)
    results = body.get("results")
    if not isinstance(results, list):
        return [], ProviderStatus(PROVIDER_OPENALEX, "error", 0, "provider_bad_json")
    records = [r for r in (parse_openalex_item(item) for item in results) if r]
    return records, ProviderStatus(PROVIDER_OPENALEX, "ok", len(records))


# ---------------------------------------------------------------------------
# Crossref
# ---------------------------------------------------------------------------


def _crossref_author_names(item: dict[str, Any]) -> list[str]:
    authors: list[str] = []
    raw = item.get("author")
    if not isinstance(raw, list):
        return []
    for entry in raw:
        if not isinstance(entry, dict):
            continue
        name = _str_field(entry.get("name"))
        if name:
            authors.append(name)
            continue
        given = _str_field(entry.get("given"))
        family = _str_field(entry.get("family"))
        if given and family:
            authors.append(f"{family}, {given}")
        elif family or given:
            authors.append(family or given)
    return authors


def parse_crossref_item(item: Any) -> dict[str, Any] | None:
    if not isinstance(item, dict):
        return None
    title = None
    raw_title = item.get("title")
    if isinstance(raw_title, list) and raw_title:
        title = _str_field(raw_title[0])
    doi_raw = _str_field(item.get("DOI"))
    year = None
    for date_field in ("published-print", "published-online", "issued"):
        dates = item.get(date_field)
        if isinstance(dates, dict):
            parts = dates.get("date-parts")
            if isinstance(parts, list) and parts and isinstance(parts[0], list) and parts[0]:
                year = _int_field(parts[0][0])
                if year is not None:
                    break
    venue = None
    container = item.get("container-title")
    if isinstance(container, list) and container:
        venue = _str_field(container[0])
    link = None
    raw_link = item.get("link")
    if isinstance(raw_link, list) and raw_link and isinstance(raw_link[0], dict):
        link = _str_field(raw_link[0].get("URL"))
    if link is None:
        resource = item.get("resource")
        if isinstance(resource, dict):
            link = _str_field(resource.get("primary", {}).get("URL"))
    abstract = _str_field(item.get("abstract"))
    if abstract and "<" in abstract:
        # Crossref abstracts often carry JATS tags; strip the two common wraps.
        for tag in ("jats:p", "p"):
            if abstract.startswith(f"<{tag}>") and abstract.endswith(f"</{tag}>"):
                abstract = abstract[len(tag) + 2 : -(len(tag) + 3)].strip()
                break
    return {
        "title": title,
        "authors": _crossref_author_names(item),
        "year": year,
        "doi": normalize_doi(doi_raw) if doi_raw else None,
        "venue": venue,
        "canonical_url": link,
        "abstract": abstract or None,
        "oa_url": None,
        "provider": PROVIDER_CROSSREF,
        "provider_id": _str_field(item.get("DOI")),
    }


def search_crossref(
    client: httpx.Client,
    query: str,
    limit: int,
    *,
    base_url: str = CROSSREF_BASE_URL,
    timeout_s: float = PROVIDER_TIMEOUT_S,
) -> tuple[list[dict[str, Any]], ProviderStatus]:
    """Query Crossref ``works``; returns (records, status) and never raises."""
    try:
        body = _fetch_json(
            client,
            base_url,
            {
                "query.bibliographic": query,
                "rows": str(limit),
                "select": ",".join(
                    [
                        "DOI",
                        "title",
                        "author",
                        "issued",
                        "published-print",
                        "published-online",
                        "container-title",
                        "link",
                        "resource",
                        "abstract",
                    ]
                ),
            },
        )
    except _ProviderHTTPError as exc:
        return [], ProviderStatus(PROVIDER_CROSSREF, "error", 0, exc.code)
    message = body.get("message")
    items = message.get("items") if isinstance(message, dict) else None
    if not isinstance(items, list):
        return [], ProviderStatus(PROVIDER_CROSSREF, "error", 0, "provider_bad_json")
    records = [r for r in (parse_crossref_item(item) for item in items) if r]
    return records, ProviderStatus(PROVIDER_CROSSREF, "ok", len(records))


# ---------------------------------------------------------------------------
# Semantic Scholar
# ---------------------------------------------------------------------------

_S2_FIELDS = "title,externalIds,authors,year,venue,abstract,url,openAccessPdf"


def _s2_paper_id(item: dict[str, Any]) -> str | None:
    external = item.get("externalIds")
    if isinstance(external, dict):
        for key in ("DOI", "CorpusId", "ArXiv"):
            value = external.get(key)
            if isinstance(value, (str, int)) and str(value).strip():
                return f"{key}:{value}"
    return _str_field(item.get("paperId"))


def _s2_oa_url(item: dict[str, Any]) -> str | None:
    oa = item.get("openAccessPdf")
    if isinstance(oa, dict):
        return _str_field(oa.get("url"))
    return None


def parse_semantic_scholar_item(item: Any) -> dict[str, Any] | None:
    if not isinstance(item, dict):
        return None
    authors: list[str] = []
    raw = item.get("authors")
    if isinstance(raw, list):
        for entry in raw:
            if isinstance(entry, dict):
                name = _str_field(entry.get("name"))
                if name:
                    authors.append(name)
    return {
        "title": _str_field(item.get("title")),
        "authors": authors,
        "year": _int_field(item.get("year")),
        "doi": None,
        "venue": _str_field(item.get("venue")),
        "canonical_url": _str_field(item.get("url")),
        "abstract": _str_field(item.get("abstract")),
        "oa_url": _s2_oa_url(item),
        "provider": PROVIDER_SEMANTIC_SCHOLAR,
        "provider_id": _s2_paper_id(item),
        "_doi_raw": _str_field(
            (item.get("externalIds") or {}).get("DOI")
            if isinstance(item.get("externalIds"), dict)
            else None
        ),
    }


def search_semantic_scholar(
    client: httpx.Client,
    query: str,
    limit: int,
    *,
    base_url: str = SEMANTIC_SCHOLAR_BASE_URL,
    timeout_s: float = PROVIDER_TIMEOUT_S,
) -> tuple[list[dict[str, Any]], ProviderStatus]:
    """Query Semantic Scholar paper search; returns (records, status)."""
    try:
        body = _fetch_json(
            client,
            base_url,
            {"query": query, "limit": str(limit), "fields": _S2_FIELDS},
        )
    except _ProviderHTTPError as exc:
        return [], ProviderStatus(PROVIDER_SEMANTIC_SCHOLAR, "error", 0, exc.code)
    data = body.get("data")
    if not isinstance(data, list):
        return [], ProviderStatus(PROVIDER_SEMANTIC_SCHOLAR, "error", 0, "provider_bad_json")
    records = []
    for item in data:
        parsed = parse_semantic_scholar_item(item)
        if not parsed:
            continue
        raw_doi = parsed.pop("_doi_raw", None)
        if raw_doi:
            parsed["doi"] = normalize_doi(raw_doi)
        records.append(parsed)
    return records, ProviderStatus(PROVIDER_SEMANTIC_SCHOLAR, "ok", len(records))


# ---------------------------------------------------------------------------
# Dispatch
# ---------------------------------------------------------------------------

PROVIDER_SEARCHERS: dict[str, Any] = {
    PROVIDER_OPENALEX: search_openalex,
    PROVIDER_CROSSREF: search_crossref,
    PROVIDER_SEMANTIC_SCHOLAR: search_semantic_scholar,
}


def search_provider(
    provider: str,
    client: httpx.Client,
    query: str,
    limit: int,
    *,
    timeout_s: float = PROVIDER_TIMEOUT_S,
) -> tuple[list[dict[str, Any]], ProviderStatus]:
    """Dispatch one provider search; unknown providers yield a typed error."""
    searcher = PROVIDER_SEARCHERS.get(provider)
    if searcher is None:
        return [], ProviderStatus(provider, "error", 0, "provider_unknown")
    return searcher(client, query, limit, timeout_s=timeout_s)
