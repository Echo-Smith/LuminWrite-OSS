package projectmemory

import (
	"fmt"
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

func TestValidateClaimReusesCandidateRules(t *testing.T) {
	claim := Claim{ClaimID: "claim_test", BatchID: "bat_test", ProjectID: "prj_test",
		Subject: " LuminBuddy ", Predicate: "Identity", Object: "非虚构写作助手",
		AsOf: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), SourceRunID: "run_test"}
	warnings, err := ValidateClaim(&claim)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("warnings=%v err=%v", warnings, err)
	}
	if claim.Subject != "luminbuddy" || claim.Predicate != "identity" {
		t.Fatalf("normalized=%#v", claim)
	}
	claim.SourceRunID = ""
	if _, err := ValidateClaim(&claim); err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("err=%v", err)
	}
}

func TestEvidenceHashIsIdempotentAndDisjoint(t *testing.T) {
	byRun := EvidenceHash("run_a", nil)
	byRunAgain := EvidenceHash(" run_a ", nil)
	if byRun != byRunAgain {
		t.Fatal("run hash must be whitespace-idempotent")
	}
	byRefs := EvidenceHash("", []string{"doc_b", "doc_a"})
	byRefsAgain := EvidenceHash("", []string{"doc_a", " doc_b "})
	if byRefs != byRefsAgain {
		t.Fatal("refs hash must be order-idempotent")
	}
	if byRun == byRefs {
		t.Fatal("run and refs citations must hash apart")
	}
}

func TestValidateEntityBirthCertificate(t *testing.T) {
	entity := Entity{EntityID: "ent_test", ProjectID: "prj_test", EntityKind: " Organization ",
		CanonicalName: " Acme Labs ", Aliases: []string{"ACME", "acme", ""}, SourceRunID: "run_test"}
	if _, err := ValidateEntity(&entity); err != nil {
		t.Fatalf("err=%v", err)
	}
	if entity.EntityKind != "organization" || entity.CanonicalName != "Acme Labs" || len(entity.Aliases) != 1 || entity.Aliases[0] != "ACME" {
		t.Fatalf("normalized=%#v", entity)
	}
	entity.Aliases = append(entity.Aliases, "Acme Labs")
	if _, err := ValidateEntity(&entity); err == nil || !strings.Contains(err.Error(), "duplicates the canonical name") {
		t.Fatalf("self alias err=%v", err)
	}
	entity.CanonicalName, entity.Aliases = "Acme Labs", []string{fmt.Sprintf("a%d", 1)}
	for i := 0; i < MaxAliases; i++ {
		entity.Aliases = append(entity.Aliases, fmt.Sprintf("alias-%d", i))
	}
	if _, err := ValidateEntity(&entity); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("alias cap err=%v", err)
	}
	entity.Aliases = entity.Aliases[:MaxAliases-1]
	entity.EntityKind = "character"
	if _, err := ValidateEntity(&entity); err == nil || !strings.Contains(err.Error(), "closed set") {
		t.Fatalf("fiction kind err=%v", err)
	}
	for _, kind := range EntityKinds() {
		if !ValidEntityKind(kind) {
			t.Fatalf("kind %q from the set must validate", kind)
		}
	}
}
