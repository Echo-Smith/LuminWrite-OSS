package profile

import (
	"strings"
	"testing"
)

func TestRenderMarkdownArticleFormat(t *testing.T) {
	tests := []struct {
		name         string
		outlineTitle string
		want         string
	}{
		{name: "model chooses title", want: "## 文章标题"},
		{name: "confirmed outline title", outlineTitle: "用户确认标题", want: "## 用户确认标题"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderMarkdownArticleFormat(tt.outlineTitle)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("format %q does not contain %q", got, tt.want)
			}
			if strings.Contains(got, ArticleSeparatorForTest) || strings.Contains(got, `{"title"`) {
				t.Fatalf("format still generates legacy protocol: %q", got)
			}
		})
	}
}

func TestStyleProfileWritingOutputUsesMarkdown(t *testing.T) {
	p := &StyleProfile{}
	p.OutputFormat.UseMarkdown = true

	for _, taskMode := range []string{"writing", "polish", "shorten", "expand"} {
		got := p.RenderOutputFormat(taskMode, "固定标题")
		if !strings.Contains(got, "## 固定标题") {
			t.Fatalf("%s format = %q", taskMode, got)
		}
	}
	for _, taskMode := range []string{"chat", "extract_points"} {
		if got := p.RenderOutputFormat(taskMode, "固定标题"); got != "" {
			t.Fatalf("%s format = %q, want empty", taskMode, got)
		}
	}
}

// Kept local to avoid importing the steps package back into profile.
const ArticleSeparatorForTest = "---ARTICLE---"
