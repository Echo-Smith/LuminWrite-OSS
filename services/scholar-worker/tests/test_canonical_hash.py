"""Golden cross-language test for the canonical input_hash (T04 contract).

The canonical JSON rule is a CROSS-LANGUAGE CONTRACT between the Go host
(``backend/internal/scholar.HashPayload``) and this worker:

    Keys of objects are recursively sorted by Unicode code point; strings are
    UTF-8 with no escaping beyond JSON's mandatory escapes (i.e. ensure_ascii
    =False semantics — non-ASCII characters appear verbatim, ``<``, ``>``,
    ``&``, ``/`` and U+2028/U+2029 are NOT escaped); separators are compact
    ``,`` and ``:``; the hash is sha256 over the UTF-8 bytes, hex-encoded,
    prefixed ``sha256:``.

Python reference: ``json.dumps(payload, sort_keys=True, ensure_ascii=False,
separators=(",", ":"))``.

The fixture below (with 中文, emoji, ``<>&`` and control characters) is fixed
in both languages: the Go test asserts the same golden digest value. If this
test ever changes the fixture or the rule, the Go golden test in
``backend/internal/scholar/contracts_test.go`` MUST be updated in the same
commit — that is the point of the golden pin.
"""

from __future__ import annotations

import hashlib
import json

from lumin_scholar.contracts import compute_input_hash

GOLDEN_FIXTURE = {
    "control": "line1\nline2\ttabctl sep",
    "empty": "",
    "flags": {"b": True, "a": False},
    "items": ["α", "beta", "中文", "emoji🚀", "<>&\"\\/"],
    "limit": 3,
    "nested": {"z": [1, 2, {"key": "value", "键": "中文"}], "a": None},
    "number": 42,
    "number_neg": -7,
    "query": "研究综述：材料科学 <b>&\"quotes\"</b> 😀",
}

GOLDEN_HASH = "sha256:7351615bcc321918801d50db36e4a061124409b5043675117c39755c5062864d"


def _canonical(payload) -> str:
    return json.dumps(payload, sort_keys=True, ensure_ascii=False, separators=(",", ":"))


def test_python_rule_matches_golden_digest():
    canonical = _canonical(GOLDEN_FIXTURE)
    digest = "sha256:" + hashlib.sha256(canonical.encode("utf-8")).hexdigest()
    assert digest == GOLDEN_HASH


def test_compute_input_hash_uses_the_contract_rule():
    assert compute_input_hash(GOLDEN_FIXTURE) == GOLDEN_HASH


def test_html_escaping_is_not_applied():
    # ensure_ascii=False semantics: <, >, & stay literal.
    canonical = _canonical({"a": "<b>&</b>"})
    assert "<b>&</b>" in canonical
    assert "\\u003c" not in canonical
