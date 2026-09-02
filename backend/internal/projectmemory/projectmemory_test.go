package projectmemory

import (
	"strings"
	"testing"
	"time"
)

func validCandidate() Candidate {
	return Candidate{CandidateID: "cand_test", BatchID: "bat_test", ProjectID: "prj_test",
		Subject: "林然", Predicate: "location", Object: "旧书店", AsOf: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		SourceRefs: []string{"doc_store"}}
}

func TestValidateCandidateNormalizesAndWarns(t *testing.T) {
	candidate := validCandidate()
	candidate.Predicate = "  Location "
	candidate.Subject = " 林  然 "
	warnings, err := ValidateCandidate(&candidate)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("warnings=%v err=%v", warnings, err)
	}
	if candidate.Predicate != "location" || candidate.Subject != "林 然" {
		t.Fatalf("normalized=%#v", candidate)
	}
}

func TestValidateCandidateRejectsForeignPredicate(t *testing.T) {
	candidate := validCandidate()
	candidate.Predicate = "loves_pizza"
	if _, err := ValidateCandidate(&candidate); err == nil || !strings.Contains(err.Error(), "controlled vocabulary") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateCandidateAllowsExtensionWithWarning(t *testing.T) {
	candidate := validCandidate()
	candidate.Predicate = "x-weather"
	warnings, err := ValidateCandidate(&candidate)
	if err != nil || len(warnings) != 1 || warnings[0] != "extended_predicate:x-weather" || !candidate.ExtendedPredicate {
		t.Fatalf("warnings=%v extended=%v err=%v", warnings, candidate.ExtendedPredicate, err)
	}
}

func TestValidateCandidateRequiresProvenance(t *testing.T) {
	candidate := validCandidate()
	candidate.SourceRefs = nil
	candidate.SourceRunID = ""
	if _, err := ValidateCandidate(&candidate); err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("err=%v", err)
	}
	candidate.SourceRunID = "run_test"
	if _, err := ValidateCandidate(&candidate); err != nil {
		t.Fatalf("run provenance rejected: %v", err)
	}
}

func TestValidateCandidateRejectsSelfRelationship(t *testing.T) {
	candidate := validCandidate()
	candidate.Predicate = "relationship"
	candidate.Object = "林然"
	if _, err := ValidateCandidate(&candidate); err == nil || !strings.Contains(err.Error(), "endpoints must differ") {
		t.Fatalf("err=%v", err)
	}
}

func TestContentKeyFoldsRelationshipPair(t *testing.T) {
	forward := ContentKey("prj_test", "林然", "relationship", "陈默", VocabularyVersion)
	reverse := ContentKey("prj_test", "陈默", "relationship", "林然", VocabularyVersion)
	if forward != reverse {
		t.Fatalf("pair not folded: %s vs %s", forward, reverse)
	}
	plain := ContentKey("prj_test", "林然", "location", "旧书店", VocabularyVersion)
	moved := ContentKey("prj_test", "林然", "location", "咖啡馆", VocabularyVersion)
	if plain == moved {
		t.Fatal("different objects must not share a content key")
	}
	if other := ContentKey("prj_other", "林然", "location", "旧书店", VocabularyVersion); other == plain {
		t.Fatal("different projects must not share a content key")
	}
	if bumped := ContentKey("prj_test", "林然", "location", "旧书店", VocabularyVersion+1); bumped == plain {
		t.Fatal("vocabulary bump must change the content key")
	}
}

func TestVocabularyIsComplete(t *testing.T) {
	names := Vocabulary()
	if len(names) != VocabularySize() {
		t.Fatalf("vocabulary=%v size=%d", names, VocabularySize())
	}
	for _, name := range []string{"identity", "constraint", "style_rule"} {
		if !ValidPredicate(name) {
			t.Fatalf("missing predicate %q", name)
		}
	}
}
