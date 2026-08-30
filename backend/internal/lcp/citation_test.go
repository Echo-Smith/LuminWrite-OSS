package lcp

import (
	"errors"
	"testing"
)

func TestParseCitationMarker(t *testing.T) {
	tests := []struct {
		marker string
		want   string
	}{
		{marker: "[[cite:source_018]]", want: "source_018"},
		{marker: "[[cite:report-2026.08]]", want: "report-2026.08"},
	}
	for _, test := range tests {
		got, err := ParseCitationMarker(test.marker)
		if err != nil {
			t.Fatalf("ParseCitationMarker(%q) error = %v", test.marker, err)
		}
		if got != test.want {
			t.Fatalf("ParseCitationMarker(%q) = %q, want %q", test.marker, got, test.want)
		}
	}
}

func TestParseCitationMarkerRejectsUncontrolledForms(t *testing.T) {
	for _, marker := range []string{
		"[[cite:]]",
		"[[cite:bad source]]",
		"[cite:source_1]",
		"[[footnote:source_1]]",
		"[[cite:source/1]]",
	} {
		t.Run(marker, func(t *testing.T) {
			_, err := ParseCitationMarker(marker)
			if err == nil {
				t.Fatal("expected invalid citation error")
			}
			var parseErr *ParseError
			if !errors.As(err, &parseErr) || parseErr.Code != ErrInvalidCitation {
				t.Fatalf("error = %v, want %s", err, ErrInvalidCitation)
			}
		})
	}
}
