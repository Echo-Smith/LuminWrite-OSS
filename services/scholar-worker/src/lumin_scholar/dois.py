"""DOI normalisation (R03 rule) shared by discovery and dedup.

Rule (specs/research-review/contracts.md §2):

1. trim surrounding whitespace;
2. strip a leading ``https://doi.org/``, ``http://doi.org/``,
   ``https://dx.doi.org/``, ``http://dx.doi.org/`` or ``doi:`` prefix
   (case-insensitive, one pass);
3. URL-decode percent escapes (e.g. ``10.1002%2F0470841559.ch1``);
4. trim again, then lowercase.

The result is the canonical comparison key; it is *not* the display form.
"""

from __future__ import annotations

import re
import urllib.parse

_PREFIX_RE = re.compile(
    r"^(?:(?:https?://)?(?:dx\.)?doi\.org/|doi:/?)",
    re.IGNORECASE,
)


def normalize_doi(raw: str) -> str:
    """Apply the R03 normalisation to one DOI-ish string."""
    value = raw.strip()
    # Loop the strip at most twice so "https://doi.org/doi:10.x/y" also lands
    # on the bare DOI; anything longer is not a recognised prefix stack.
    for _ in range(2):
        stripped = _PREFIX_RE.sub("", value, count=1)
        if stripped == value:
            break
        value = stripped.strip()
    decoded = urllib.parse.unquote(value).strip()
    return decoded.lower()
