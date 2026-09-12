package arreview

import (
	"testing"
)

func validInput() IdempotencyInput {
	return IdempotencyInput{
		Owner:               "user_1",
		ContractHash:        "sha256:" + repeat('a', 64),
		EvidencePackHash:    "sha256:" + repeat('b', 64),
		ApprovedOutlineHash: "sha256:" + repeat('c', 64),
		GeneratorVersion:    GeneratorVersion,
		Mode:                ModeGenerateOnly,
	}
}

func repeat(char byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = char
	}
	return string(out)
}

func TestIdempotencyDerivationIsDeterministic(t *testing.T) {
	first, err := validInput().SurrogateProjectID()
	if err != nil {
		t.Fatalf("SurrogateProjectID: %v", err)
	}
	second, _ := validInput().SurrogateProjectID()
	if first != second {
		t.Fatalf("same inputs must derive the same project id: %s vs %s", first, second)
	}
	if !ValidProjectID(first) {
		t.Fatalf("project id %q must match the surrogate pattern", first)
	}
	key, err := validInput().IdempotencyKey()
	if err != nil {
		t.Fatalf("IdempotencyKey: %v", err)
	}
	if len(key) != len("ar012-")+64 {
		t.Fatalf("key must be ar012-<sha256 hex>, got %q", key)
	}
}

func TestIdempotencySeparatesDifferentInputs(t *testing.T) {
	base := validInput()
	variants := []func(*IdempotencyInput){
		func(in *IdempotencyInput) { in.Owner = "user_2" },
		func(in *IdempotencyInput) { in.ContractHash = "sha256:" + repeat('d', 64) },
		func(in *IdempotencyInput) { in.EvidencePackHash = "sha256:" + repeat('e', 64) },
		func(in *IdempotencyInput) { in.ApprovedOutlineHash = "sha256:" + repeat('f', 64) },
		func(in *IdempotencyInput) { in.GeneratorVersion = "ar012-review@other" },
		func(in *IdempotencyInput) { in.Mode = ModeCompareOnly },
	}
	baseID, _ := base.SurrogateProjectID()
	for index, mutate := range variants {
		variant := base
		mutate(&variant)
		id, err := variant.SurrogateProjectID()
		if err != nil {
			t.Fatalf("variant %d: %v", index, err)
		}
		if id == baseID {
			t.Fatalf("variant %d must not collide with base project id", index)
		}
	}
}

func TestIdempotencyValidatesInputs(t *testing.T) {
	cases := []func(*IdempotencyInput){
		func(in *IdempotencyInput) { in.Owner = " " },
		func(in *IdempotencyInput) { in.ContractHash = "not-a-hash" },
		func(in *IdempotencyInput) { in.EvidencePackHash = "sha256:xyz" },
		func(in *IdempotencyInput) { in.ApprovedOutlineHash = "" },
		func(in *IdempotencyInput) { in.GeneratorVersion = "" },
		func(in *IdempotencyInput) { in.Mode = "mode:unknown" },
	}
	for index, mutate := range cases {
		in := validInput()
		mutate(&in)
		if _, err := in.SurrogateProjectID(); err == nil {
			t.Fatalf("variant %d must fail validation", index)
		}
	}
}

func TestOwnerWithSeparatorsDoesNotAlias(t *testing.T) {
	first := validInput()
	first.Owner = "user:1\x1fx"
	second := validInput()
	second.Owner = "user"
	second.ContractHash = "sha256:" + repeat('a', 64)
	second.EvidencePackHash = "sha256:" + repeat('b', 64)
	second.ApprovedOutlineHash = "sha256:" + repeat('c', 64)
	// Different owners must derive different digests even when the remaining
	// fields could be shifted to imitate one another.
	idA, errA := first.SurrogateProjectID()
	idB, errB := second.SurrogateProjectID()
	if errA != nil || errB != nil {
		t.Fatalf("derive: %v %v", errA, errB)
	}
	if idA == idB {
		t.Fatal("owner separator aliasing detected")
	}
}
