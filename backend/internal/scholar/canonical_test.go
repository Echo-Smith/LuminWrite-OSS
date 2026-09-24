package scholar

import (
	"encoding/json"
	"strings"
	"testing"
)

// The golden fixture pins the canonical JSON digest (frozen since the T04
// contract). It covers 中文, emoji, <>&, quotes, backslash, control
// characters, nested maps/arrays, bools and nulls. Treat fixture and pinned
// digest as immutable — persisted input_hash values must stay comparable
// across releases.
func goldenFixture() map[string]any {
	return map[string]any{
		"control": "line1\nline2\ttab\x01ctl sep",
		"empty":   "",
		"flags":   map[string]any{"b": true, "a": false},
		"items":   []any{"α", "beta", "中文", "emoji🚀", "<>&\"\\/"},
		"limit":   3,
		"nested": map[string]any{
			"z": []any{int64(1), int64(2), map[string]any{"key": "value", "键": "中文"}},
			"a": nil,
		},
		"number":     42,
		"number_neg": -7,
		"query":      "研究综述：材料科学 <b>&\"quotes\"</b> 😀",
	}
}

const goldenHash = "sha256:7351615bcc321918801d50db36e4a061124409b5043675117c39755c5062864d"

func TestHashPayloadGoldenCrossLanguage(t *testing.T) {
	got, err := HashPayload(goldenFixture())
	if err != nil {
		t.Fatalf("HashPayload: %v", err)
	}
	if got != goldenHash {
		t.Fatalf("golden hash mismatch:\n got  %s\n want %s", got, goldenHash)
	}
}

func TestCanonicalJSONNoHTMLEscaping(t *testing.T) {
	encoded, err := CanonicalPayloadJSON(map[string]any{"a": "<b>&</b>"})
	if err != nil {
		t.Fatalf("CanonicalPayloadJSON: %v", err)
	}
	if !strings.Contains(string(encoded), "<b>&</b>") {
		t.Fatalf("canonical form must not HTML-escape: %s", encoded)
	}
	if strings.Contains(string(encoded), `\u003c`) {
		t.Fatalf("canonical form must not contain \\u003c: %s", encoded)
	}
}

func TestCanonicalJSONKeyOrderSorted(t *testing.T) {
	encoded, err := CanonicalPayloadJSON(map[string]any{
		"b": 1, "a": 2, "中文": 3, "Alpha": 4,
	})
	if err != nil {
		t.Fatalf("CanonicalPayloadJSON: %v", err)
	}
	want := `{"Alpha":4,"a":2,"b":1,"中文":3}`
	if string(encoded) != want {
		t.Fatalf("canonical = %s, want %s", encoded, want)
	}
}

func TestHashPayloadRejectsFloats(t *testing.T) {
	if _, err := HashPayload(map[string]any{"x": 1.5}); err == nil {
		t.Fatal("float payloads must be rejected (fail closed)")
	}
	if _, err := HashPayload(map[string]any{"x": float64(2)}); err == nil {
		t.Fatal("float64 payloads must be rejected even for integral values")
	}
	// json.Number is the documented escape hatch for pre-formatted numbers.
	got, err := HashPayload(map[string]any{"x": json.Number("1.5")})
	if err != nil {
		t.Fatalf("json.Number should be accepted: %v", err)
	}
	want := "sha256:" + "8bda2e39e7e78b8f5a1b8d9e60e5f7f4ec1a2b3c" // not pinned; just must compute
	if !strings.HasPrefix(got, "sha256:") || len(want) == 0 {
		t.Fatalf("unexpected hash %q", got)
	}
}

func TestCanonicalJSONMatchesPythonForNestedControlChars(t *testing.T) {
	// Second pin: smaller fixture whose digest was pinned during T04 (the
	// retired Python worker produced the same value; kept as a regression pin).
	payload := map[string]any{
		"a": "<b>&</b>",
		"c": "ctl\x01\uff0c中文",
		"n": nil,
		"l": []any{true, false, nil, "eof\f\bgone\r"},
	}
	const want = "sha256:22f4965a282a538338dba9d1e1e97bc457fe23a10c2e2b12c34fa1c1316001e0"
	got, err := HashPayload(payload)
	if err != nil {
		t.Fatalf("HashPayload: %v", err)
	}
	if got != want {
		t.Fatalf("second pin mismatch:\n got  %s\n want %s", got, want)
	}
}
