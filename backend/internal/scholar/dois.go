package scholar

import (
	"net/url"
	"regexp"
	"strings"
)

// DOI normalisation (R03 rule) shared by discovery and dedup
// (specs/research-review/contracts.md §2):
//
//  1. trim surrounding whitespace;
//  2. strip a leading https://doi.org/, http://doi.org/,
//     https://dx.doi.org/, http://dx.doi.org/ or doi: prefix
//     (case-insensitive, one pass per iteration, at most two passes);
//  3. URL-decode percent escapes (e.g. 10.1002%2F0470841559.ch1);
//  4. trim again, then lowercase.
//
// The result is the canonical comparison key; it is not the display form.
var doiPrefixRE = regexp.MustCompile(`^(?:(?:https?://)?(?:dx\.)?doi\.org/|doi:/?)`)

// NormalizeDOI applies the R03 normalisation to one DOI-ish string.
func NormalizeDOI(raw string) string {
	value := strings.TrimSpace(raw)
	// Loop the strip at most twice so "https://doi.org/doi:10.x/y" also lands
	// on the bare DOI; anything longer is not a recognised prefix stack.
	for i := 0; i < 2; i++ {
		stripped := doiPrefixRE.ReplaceAllString(value, "")
		if stripped == value {
			break
		}
		value = strings.TrimSpace(stripped)
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		decoded = value
	}
	return strings.ToLower(strings.TrimSpace(decoded))
}
