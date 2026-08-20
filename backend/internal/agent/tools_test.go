package agent

import (
	"reflect"
	"testing"
)

func TestExtractKeywordsSplitsStraightAndChineseQuotes(t *testing.T) {
	got := extractKeywords(`"alpha" 'beta' “gamma”`)
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractKeywords() = %v, want %v", got, want)
	}
}
