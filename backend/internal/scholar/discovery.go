package scholar

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// RawProviderRecord is one normalised record as extracted from a provider
// response, before cross-provider merging. Mirrors the record dicts the
// provider adapters emitted.
type RawProviderRecord struct {
	Provider     string
	ProviderID   string
	Title        *string
	Authors      []string
	Year         *int64
	DOI          string // already normalised by the adapter when present
	Abstract     *string
	Venue        *string
	CanonicalURL *string
	OAURL        *string
}

// mergeRecords folds raw provider records into PaperCandidate rows using the
// R03 dedup rule (contracts.md §2):
//
//   - Cross-provider merge happens ONLY on exact normalised-DOI equality, or
//     on (normalised title + first-author family name + year) all matching.
//   - Conflicting DOIs with similar titles stay separate records, each
//     carrying a possible_duplicate hint.
//   - Records with no DOI and no confirmed title/author/year match stay
//     separate.
//
// Returns (papers, warnings).
func mergeRecords(records []RawProviderRecord) ([]PaperCandidate, []string) {
	papers := make([]PaperCandidate, 0, len(records))
	warnings := []string{}

	// Indexes for the two allowed merge paths.
	byDOI := map[string]int{}
	type tayKey struct{ title, family string; year int64 }
	byTitleAuthorYear := map[tayKey]int{}

	for _, record := range records {
		provider := record.Provider
		if provider == "" {
			provider = "unknown"
		}
		doi := ""
		if record.DOI != "" {
			// Defensive re-normalisation: adapters already normalise, but
			// merge correctness depends on the R03 key, so never trust
			// upstream casing or URL prefixes here.
			doi = NormalizeDOI(record.DOI)
		}

		aliases := []string{}
		if record.ProviderID != "" {
			aliases = append(aliases, record.ProviderID)
		}
		if doi != "" {
			aliases = append(aliases, "doi:"+doi)
		}

		matched := -1
		if doi != "" {
			if idx, ok := byDOI[doi]; ok {
				matched = idx
			}
		}
		if matched < 0 && record.Title != nil && len(record.Authors) > 0 && record.Year != nil {
			key := tayKey{titleKey(*record.Title), familyName(record.Authors[0]), *record.Year}
			if idx, ok := byTitleAuthorYear[key]; ok {
				matched = idx
			}
		}

		if matched >= 0 {
			paper := &papers[matched]
			if paper.DOI != nil && doi != "" && *paper.DOI != doi {
				// Different DOI with a title match: per R03 this is exactly
				// the ambiguous case — keep both records separate.
				matched = -1
			} else {
				mergeInto(paper, provider, aliases, record)
			}
		}
		if matched < 0 {
			paper := PaperCandidate{
				DOI:               nullableString(doi),
				Title:             record.Title,
				Authors:           append([]string(nil), record.Authors...),
				Year:              record.Year,
				Venue:             record.Venue,
				CanonicalURL:      record.CanonicalURL,
				Aliases:           aliases,
				Abstract:          record.Abstract,
				OAURL:             record.OAURL,
				Providers:         []string{provider},
				PossibleDuplicate: false,
			}
			paper.PaperID = makePaperID(doi, record.Title)
			papers = append(papers, paper)
			index := len(papers) - 1
			if doi != "" {
				byDOI[doi] = index
			}
			if record.Title != nil && len(record.Authors) > 0 && record.Year != nil {
				key := tayKey{titleKey(*record.Title), familyName(record.Authors[0]), *record.Year}
				byTitleAuthorYear[key] = index
			}
		}
	}

	// Same title arriving under two different DOIs -> mark both as possible
	// duplicates instead of merging (R03: conflicting DOIs are not merged).
	seenTitles := map[string]int{}
	for index := range papers {
		if papers[index].Title == nil {
			continue
		}
		tkey := titleKey(*papers[index].Title)
		first, seen := seenTitles[tkey]
		if !seen {
			seenTitles[tkey] = index
			continue
		}
		if papers[first].DOI != nil && papers[index].DOI != nil && *papers[first].DOI != *papers[index].DOI {
			papers[first].PossibleDuplicate = true
			papers[index].PossibleDuplicate = true
			warnings = append(warnings,
				"possible duplicate papers kept separate (conflicting DOIs): "+
					papers[first].PaperID+" vs "+papers[index].PaperID)
		}
	}

	return papers, warnings
}

func mergeInto(paper *PaperCandidate, provider string, aliases []string, record RawProviderRecord) {
	seenProvider := false
	for _, p := range paper.Providers {
		if p == provider {
			seenProvider = true
			break
		}
	}
	if !seenProvider {
		paper.Providers = append(paper.Providers, provider)
	}
	for _, alias := range aliases {
		known := false
		for _, a := range paper.Aliases {
			if a == alias {
				known = true
				break
			}
		}
		if !known {
			paper.Aliases = append(paper.Aliases, alias)
		}
	}
	// Fill gaps only; first-seen values win for display fields so output is
	// deterministic for a given provider ordering.
	if (paper.Abstract == nil || *paper.Abstract == "") && record.Abstract != nil {
		paper.Abstract = record.Abstract
	}
	if (paper.Venue == nil || *paper.Venue == "") && record.Venue != nil {
		paper.Venue = record.Venue
	}
	if (paper.CanonicalURL == nil || *paper.CanonicalURL == "") && record.CanonicalURL != nil {
		paper.CanonicalURL = record.CanonicalURL
	}
	if (paper.OAURL == nil || *paper.OAURL == "") && record.OAURL != nil {
		paper.OAURL = record.OAURL
	}
	if (paper.Title == nil || *paper.Title == "") && record.Title != nil {
		paper.Title = record.Title
	}
	if paper.Year == nil && record.Year != nil {
		paper.Year = record.Year
	}
	if len(paper.Authors) == 0 && len(record.Authors) > 0 {
		paper.Authors = append([]string(nil), record.Authors...)
	}
}

// familyName is best-effort family-name extraction for dedup keys (not
// display). "Family, Given" and "Given Family" both resolve to the family
// name, lowercased.
func familyName(author string) string {
	name := strings.TrimSpace(author)
	if name == "" {
		return ""
	}
	if idx := strings.IndexAny(name, ","); idx >= 0 {
		fields := strings.FieldsFunc(name[:idx], func(r rune) bool { return r == ' ' || r == '\t' })
		if len(fields) > 0 {
			return strings.ToLower(fields[0])
		}
		return ""
	}
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return ""
	}
	return strings.ToLower(parts[len(parts)-1])
}

// titleKey is the loose title normalisation for the author/year-assisted
// match: keep ASCII letters/digits and CJK, drop everything else, lowercase.
var titleKeyRE = regexp.MustCompile(`[^a-z0-9\x{4e00}-\x{9fff}]+`)

func titleKey(title string) string {
	return titleKeyRE.ReplaceAllString(strings.ToLower(title), "")
}

// makePaperID is the stable, content-derived paper identity: the same work
// maps to the same id across runs when the merging keys agree. Format:
// "p_<12 hex>" — from the normalised DOI when present, else from the
// normalised title.
func makePaperID(doi string, title *string) string {
	var material string
	if doi != "" {
		material = "doi:" + doi
	} else if title != nil {
		material = "title:" + titleKey(*title)
	}
	digest := sha256.Sum256([]byte(material))
	return "p_" + hex.EncodeToString(digest[:])[:12]
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
