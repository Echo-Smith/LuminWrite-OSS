package database

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func freezeTestBatch(batch *WABenchFrozenBatch) {
	encoded, _ := json.Marshal(batch.CaseRefs)
	batch.ContentHash = fmt.Sprintf("sha256:%x", sha256.Sum256(encoded))
}

func TestValidatePortableBatchRejectsIdentityDrift(t *testing.T) {
	batch := WABenchFrozenBatch{SchemaVersion: WABenchSchemaVersion, BatchID: "batch", Version: "v1.0.0", Visibility: "public", SuiteID: "wabench.public.test"}
	batch.CaseRefs = append(batch.CaseRefs, WABenchFrozenCaseRef{CaseID: "case_one", InputHash: "sha256:expected", PrivacyLevel: "synthetic", SourceFixtureRefs: []string{}})
	freezeTestBatch(&batch)
	cases := []WABenchPortableCase{{CaseID: "case_one", InputHash: "sha256:changed", PrivacyLevel: "synthetic"}}
	if err := validatePortableBatch(batch, cases, nil); err == nil {
		t.Fatal("input hash drift was accepted")
	}
}

func TestPrivateBatchAndResolverKeepRedactedTextOutsideDatabase(t *testing.T) {
	input := "脱敏后的真实业务写作需求"
	redactedHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(input)))
	originHash := "sha256:" + strings.Repeat("a", 64)
	row := WABenchPrivateHoldoutRecord{HoldoutID: "rbh_case", InputHash: originHash, RedactedInputHash: redactedHash, InputRedacted: input, TaskType: "writing"}
	batch := WABenchFrozenBatch{SchemaVersion: WABenchSchemaVersion, BatchID: "private_batch", Version: "v1.0.0", Visibility: "private", SuiteID: "luminbuddy.private.test"}
	batch.CaseRefs = []WABenchFrozenCaseRef{{CaseID: row.HoldoutID, InputHash: redactedHash, OriginInputHash: originHash, PrivacyLevel: "anonymized", SourceFixtureRefs: []string{}, PrivateCaseRef: "private.jsonl#rbh_case"}}
	freezeTestBatch(&batch)
	if err := validatePrivateBatch(batch, []WABenchPrivateHoldoutRecord{row}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "holdout.jsonl")
	encoded, _ := json.Marshal(row)
	if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewJSONLWABenchPrivateInputResolver(path)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewWABenchRepo(nil, resolver)
	got, err := repo.ResolveCaseInput(context.Background(), WABenchCase{CaseID: row.HoldoutID, InputStorage: "private_ref", InputRef: wabenchPrivateHoldoutPrefix + row.HoldoutID, InputHash: originHash, RedactedInputHash: redactedHash})
	if err != nil || got != input {
		t.Fatalf("resolved input mismatch: got=%q err=%v", got, err)
	}
	if _, err := repo.ResolveCaseInput(context.Background(), WABenchCase{CaseID: row.HoldoutID, InputStorage: "private_ref", InputRef: wabenchPrivateHoldoutPrefix + row.HoldoutID, RedactedInputHash: "sha256:" + strings.Repeat("b", 64)}); err == nil {
		t.Fatal("resolver accepted a stale redacted input hash")
	}
}
