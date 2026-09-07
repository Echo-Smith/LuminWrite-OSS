"""DOI normalisation tests (R03 rule, contracts.md §2)."""

from __future__ import annotations

import pytest

from lumin_scholar.dois import normalize_doi


class TestNormalizeDoi:
    @pytest.mark.parametrize(
        ("raw", "expected"),
        [
            ("10.1000/j.journal.2024.001", "10.1000/j.journal.2024.001"),
            ("  10.1000/j.journal.2024.001  ", "10.1000/j.journal.2024.001"),
            ("10.1000/UPPER-Case", "10.1000/upper-case"),
            ("https://doi.org/10.1000/abc", "10.1000/abc"),
            ("http://doi.org/10.1000/abc", "10.1000/abc"),
            ("https://dx.doi.org/10.1000/abc", "10.1000/abc"),
            ("doi:10.1000/abc", "10.1000/abc"),
            ("DOI:10.1000/abc", "10.1000/abc"),
            ("10.1002%2F0470841559.ch1", "10.1002/0470841559.ch1"),
            ("https://doi.org/10.1002%2F0470841559.ch1", "10.1002/0470841559.ch1"),
            ("https://doi.org/doi:10.1000/nested", "10.1000/nested"),
        ],
    )
    def test_normalisation_rules(self, raw, expected):
        assert normalize_doi(raw) == expected
