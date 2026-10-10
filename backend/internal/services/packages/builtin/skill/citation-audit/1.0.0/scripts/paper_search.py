#!/usr/bin/env python3
"""Multi-source paper search: arXiv + Semantic Scholar + OpenAlex + Crossref.

Stdlib only, no API keys. Unified output schema, cross-source dedup by
DOI / arXiv id / normalized title. Outputs to stdout only — this script
never writes files; persist results with the platform's own file tools.

Usage:
  paper_search.py "query terms" [--sources arxiv,s2,openalex,crossref] [--limit 10]
                  [--year-from 2020]
"""
import argparse
import ipaddress
import json
import re
import socket
import sys
import time
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET

UA = {"User-Agent": "LuminBuddy-citation-audit/1.0 (mailto:research@example.org)"}
MAX_BYTES = 20 * 1024 * 1024

# ── SSRF guard: https/http only, and every resolved IP must be public ──

ALLOWED_SCHEMES = {"https", "http"}


def _is_public_ip(ip: str) -> bool:
    addr = ipaddress.ip_address(ip)
    return not (
        addr.is_private
        or addr.is_loopback
        or addr.is_link_local
        or addr.is_reserved
        or addr.is_multicast
        or addr.is_unspecified
    )


def validate_url(url: str) -> None:
    parts = urllib.parse.urlsplit(url)
    if parts.scheme not in ALLOWED_SCHEMES:
        sys.exit(f"Blocked non-http(s) URL: {url}")
    host = parts.hostname
    if not host:
        sys.exit(f"Blocked URL without host: {url}")
    try:
        infos = socket.getaddrinfo(host, parts.port or (443 if parts.scheme == "https" else 80),
                                   proto=socket.IPPROTO_TCP)
    except socket.gaierror as e:
        sys.exit(f"Cannot resolve {host}: {e}")
    for info in infos:
        if not _is_public_ip(info[4][0]):
            sys.exit(f"Blocked URL resolving to private/reserved address: {url}")


class _GuardedRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        validate_url(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


_OPENER = urllib.request.build_opener(_GuardedRedirect)


def http_get(url, retries=3):
    validate_url(url)
    for i in range(retries):
        try:
            req = urllib.request.Request(url, headers=UA)
            with _OPENER.open(req, timeout=30) as r:
                return r.read(MAX_BYTES + 1).decode("utf-8", "replace")
        except Exception as e:
            code = getattr(e, "code", None)
            if i < retries - 1 and (code is None or code == 429 or code >= 500):
                time.sleep(2 ** (i + 1))
                continue
            print(f"[warn] GET failed ({e}): {url}", file=sys.stderr)
            return None
    return None


def parse_atom_xml(body: str):
    if len(body) > MAX_BYTES:
        sys.exit("Response too large to parse safely.")
    lowered = body.lower()
    if "<!doctype" in lowered or "<!entity" in lowered:
        sys.exit("Rejected XML with DTD/entity declarations (entity-expansion guard).")
    return ET.fromstring(body)


def norm_title(t):
    return re.sub(r"[^a-z0-9]", "", (t or "").lower())


def search_arxiv(query, limit, year_from):
    q = urllib.parse.quote(query)
    url = (f"https://export.arxiv.org/api/query?search_query=all:{q}"
           f"&max_results={limit}&sortBy=relevance")
    body = http_get(url)
    if not body:
        return []
    ns = {"a": "http://www.w3.org/2005/Atom"}
    out = []
    for e in parse_atom_xml(body).findall("a:entry", ns):
        year = int((e.findtext("a:published", "", ns) or "0000")[:4] or 0)
        if year_from and year and year < year_from:
            continue
        aid = (e.findtext("a:id", "", ns) or "").rsplit("/abs/", 1)[-1]
        out.append({
            "title": re.sub(r"\s+", " ", e.findtext("a:title", "", ns)).strip(),
            "authors": [a.findtext("a:name", "", ns) for a in e.findall("a:author", ns)],
            "year": year or None,
            "abstract": re.sub(r"\s+", " ", e.findtext("a:summary", "", ns)).strip(),
            "doi": None,
            "arxiv_id": re.sub(r"v\d+$", "", aid),
            "url": f"https://arxiv.org/abs/{aid}",
            "venue": "arXiv",
            "citations": None,
            "source": "arxiv",
        })
    return out


def search_s2(query, limit, year_from):
    q = urllib.parse.quote(query)
    url = (f"https://api.semanticscholar.org/graph/v1/paper/search?query={q}"
           f"&limit={limit}&fields=title,authors,year,abstract,externalIds,url,venue,citationCount")
    if year_from:
        url += f"&year={year_from}-"
    body = http_get(url)
    if not body:
        return []
    out = []
    for p in json.loads(body).get("data", []):
        ext = p.get("externalIds") or {}
        out.append({
            "title": p.get("title"),
            "authors": [a.get("name") for a in (p.get("authors") or [])],
            "year": p.get("year"),
            "abstract": p.get("abstract"),
            "doi": ext.get("DOI"),
            "arxiv_id": ext.get("ArXiv"),
            "url": p.get("url"),
            "venue": p.get("venue"),
            "citations": p.get("citationCount"),
            "source": "s2",
        })
    return out


def search_openalex(query, limit, year_from):
    q = urllib.parse.quote(query)
    flt = f"&filter=from_publication_date:{year_from}-01-01" if year_from else ""
    url = f"https://api.openalex.org/works?search={q}&per-page={limit}{flt}"
    body = http_get(url)
    if not body:
        return []
    out = []
    for w in json.loads(body).get("results", []):
        abstract = None
        inv = w.get("abstract_inverted_index")
        if inv:
            pos = {p: word for word, ps in inv.items() for p in ps}
            abstract = " ".join(pos[i] for i in sorted(pos))
        loc = (w.get("primary_location") or {}).get("source") or {}
        out.append({
            "title": w.get("title"),
            "authors": [a["author"]["display_name"] for a in (w.get("authorships") or [])],
            "year": w.get("publication_year"),
            "abstract": abstract,
            "doi": (w.get("doi") or "").replace("https://doi.org/", "") or None,
            "arxiv_id": None,
            "url": w.get("doi") or w.get("id"),
            "venue": loc.get("display_name"),
            "citations": w.get("cited_by_count"),
            "source": "openalex",
        })
    return out


def search_crossref(query, limit, year_from):
    q = urllib.parse.quote(query)
    flt = f"&filter=from-pub-date:{year_from}-01-01" if year_from else ""
    url = f"https://api.crossref.org/works?query={q}&rows={limit}{flt}"
    body = http_get(url)
    if not body:
        return []
    out = []
    for it in json.loads(body).get("message", {}).get("items", []):
        year = None
        for k in ("published-print", "published-online", "issued"):
            parts = (it.get(k) or {}).get("date-parts") or [[None]]
            if parts[0][0]:
                year = parts[0][0]
                break
        out.append({
            "title": (it.get("title") or [None])[0],
            "authors": [f"{a.get('given','')} {a.get('family','')}".strip()
                        for a in (it.get("author") or [])],
            "year": year,
            "abstract": re.sub(r"<[^>]+>", "", it.get("abstract") or "") or None,
            "doi": it.get("DOI"),
            "arxiv_id": None,
            "url": it.get("URL"),
            "venue": (it.get("container-title") or [None])[0],
            "citations": it.get("is-referenced-by-count"),
            "source": "crossref",
        })
    return out


SEARCHERS = {"arxiv": search_arxiv, "s2": search_s2,
             "openalex": search_openalex, "crossref": search_crossref}


def dedup(papers):
    seen, out = {}, []
    for p in papers:
        key = (p.get("doi") or "").lower() or p.get("arxiv_id") or norm_title(p.get("title"))
        if not key:
            continue
        if key in seen:
            prev = seen[key]
            for f in ("doi", "arxiv_id", "abstract", "venue", "citations"):
                if not prev.get(f) and p.get(f):
                    prev[f] = p[f]
            prev["source"] += "," + p["source"]
        else:
            seen[key] = p
            out.append(p)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("query")
    ap.add_argument("--sources", default="arxiv,s2,openalex")
    ap.add_argument("--limit", type=int, default=10)
    ap.add_argument("--year-from", type=int, default=None)
    args = ap.parse_args()

    papers = []
    for s in args.sources.split(","):
        s = s.strip()
        if s not in SEARCHERS:
            print(f"[warn] unknown source: {s}", file=sys.stderr)
            continue
        got = SEARCHERS[s](args.query, args.limit, args.year_from)
        print(f"[info] {s}: {len(got)} results", file=sys.stderr)
        papers.extend(got)

    papers = dedup(papers)
    papers.sort(key=lambda p: (p.get("citations") or 0), reverse=True)
    print(json.dumps(papers, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
