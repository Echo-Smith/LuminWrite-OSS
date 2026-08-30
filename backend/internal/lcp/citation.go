package lcp

import (
	"fmt"
	"regexp"
)

var citationMarkerPattern = regexp.MustCompile(`^\[\[cite:([A-Za-z0-9][A-Za-z0-9_.-]{0,127})\]\]$`)

func ParseCitationMarker(marker string) (string, error) {
	match := citationMarkerPattern.FindStringSubmatch(marker)
	if match == nil {
		return "", &ParseError{Code: ErrInvalidCitation, Message: fmt.Sprintf("invalid citation marker %q", marker)}
	}
	return match[1], nil
}
