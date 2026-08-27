package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
)

func TestExtractKeywordsSplitsStraightAndChineseQuotes(t *testing.T) {
	got := extractKeywords(`"alpha" 'beta' “gamma”`)
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractKeywords() = %v, want %v", got, want)
	}
}

func TestArticleSignalToolsRepeatNearEndOutputContract(t *testing.T) {
	tests := []struct {
		name string
		run  func() (string, error)
	}{
		{name: "write article", run: func() (string, error) {
			return executeWriteArticle(ToolExecutorConfig{}, `{"topic":"测试"}`)
		}},
		{name: "revise section", run: func() (string, error) {
			return executeReviseSection(ToolExecutorConfig{}, `{"section_hint":"开头","instruction":"更简洁"}`)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.run()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(result, profile.MarkdownArticleOutputReminder) {
				t.Fatalf("tool result lacks near-end reminder: %q", result)
			}
			if strings.Contains(result, "---ARTICLE---") || strings.Contains(result, `{"title"`) {
				t.Fatalf("tool result generates legacy protocol: %q", result)
			}
		})
	}
}
