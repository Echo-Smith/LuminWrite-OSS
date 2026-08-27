package steps

import (
	"strings"
	"testing"
)

func TestArticleStreamParserProtocols(t *testing.T) {
	tests := []struct {
		name           string
		chunks         []string
		confirmedTitle string
		maxPrefixRunes int
		wantTitle      string
		wantBody       string
		wantProtocol   ArticleOutputProtocol
	}{
		{
			name:         "canonical markdown in one delta",
			chunks:       []string{"## 春天里的信\n\n第一段。\n第二段。"},
			wantTitle:    "春天里的信",
			wantBody:     "第一段。\n第二段。",
			wantProtocol: ArticleProtocolMarkdown,
		},
		{
			name:         "markdown heading split across deltas",
			chunks:       []string{"#", "# 被拆", "开的标题", "\n", "\n正文开始。"},
			wantTitle:    "被拆开的标题",
			wantBody:     "正文开始。",
			wantProtocol: ArticleProtocolMarkdown,
		},
		{
			name:         "preamble before markdown heading",
			chunks:       []string{"好的，以下是文章：\n## 真正标题\n\n正文。"},
			wantTitle:    "真正标题",
			wantBody:     "正文。",
			wantProtocol: ArticleProtocolMarkdown,
		},
		{
			name:         "single hash compatibility",
			chunks:       []string{"# 一级标题\n正文。"},
			wantTitle:    "一级标题",
			wantBody:     "正文。",
			wantProtocol: ArticleProtocolMarkdown,
		},
		{
			name:         "legacy json compatibility",
			chunks:       []string{"{\"title\":\"旧标题\"}\n---ART", "ICLE---\n旧正文。"},
			wantTitle:    "旧标题",
			wantBody:     "旧正文。",
			wantProtocol: ArticleProtocolLegacyJSON,
		},
		{
			name:           "confirmed title when model omits heading",
			chunks:         []string{"这是一段没有标题的正文，而且长度足以触发透传。"},
			confirmedTitle: "用户确认标题",
			maxPrefixRunes: 8,
			wantTitle:      "用户确认标题",
			wantBody:       "这是一段没有标题的正文，而且长度足以触发透传。",
			wantProtocol:   ArticleProtocolMissingTitle,
		},
		{
			name:           "short first line fallback",
			chunks:         []string{"没有井号的短标题\n正文内容已经足够长。"},
			maxPrefixRunes: 8,
			wantTitle:      "没有井号的短标题",
			wantBody:       "正文内容已经足够长。",
			wantProtocol:   ArticleProtocolShortLineFallback,
		},
		{
			name:           "missing title preserves every byte",
			chunks:         []string{"这是正文第一句。它不是标题，而且必须完整保留。"},
			maxPrefixRunes: 8,
			wantBody:       "这是正文第一句。它不是标题，而且必须完整保留。",
			wantProtocol:   ArticleProtocolMissingTitle,
		},
		{
			name:         "finalize completes heading without newline",
			chunks:       []string{"## 只有标题"},
			wantTitle:    "只有标题",
			wantBody:     "",
			wantProtocol: ArticleProtocolMarkdown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var titles []string
			var streamed strings.Builder
			parser := NewArticleStreamParser(ArticleStreamParserConfig{
				ConfirmedTitle: tt.confirmedTitle,
				MaxPrefixRunes: tt.maxPrefixRunes,
				OnTitle: func(title string) {
					titles = append(titles, title)
				},
				OnBody: func(delta string) {
					streamed.WriteString(delta)
				},
			})

			for _, chunk := range tt.chunks {
				parser.Push(chunk)
			}
			result := parser.Finalize(strings.Join(tt.chunks, ""))

			if result.Title != tt.wantTitle {
				t.Fatalf("title = %q, want %q", result.Title, tt.wantTitle)
			}
			if result.Body != tt.wantBody {
				t.Fatalf("body = %q, want %q", result.Body, tt.wantBody)
			}
			if streamed.String() != tt.wantBody {
				t.Fatalf("streamed body = %q, want %q", streamed.String(), tt.wantBody)
			}
			if result.Protocol != tt.wantProtocol {
				t.Fatalf("protocol = %q, want %q", result.Protocol, tt.wantProtocol)
			}
			wantTitleEvents := 0
			if tt.wantTitle != "" {
				wantTitleEvents = 1
			}
			if len(titles) != wantTitleEvents {
				t.Fatalf("title events = %v, want %d event(s)", titles, wantTitleEvents)
			}
			if wantTitleEvents == 1 && titles[0] != tt.wantTitle {
				t.Fatalf("emitted title = %q, want %q", titles[0], tt.wantTitle)
			}
		})
	}
}

func TestArticleStreamParserDoesNotDuplicateBodyDelta(t *testing.T) {
	var streamed strings.Builder
	parser := NewArticleStreamParser(ArticleStreamParserConfig{
		OnBody: func(delta string) { streamed.WriteString(delta) },
	})

	parser.Push("## 标题\n\n正文首段")
	parser.Push("，继续正文。")
	result := parser.Finalize("## 标题\n\n正文首段，继续正文。")

	const want = "正文首段，继续正文。"
	if result.Body != want || streamed.String() != want {
		t.Fatalf("body duplicated: result=%q streamed=%q", result.Body, streamed.String())
	}
}

func TestArticleStreamParserResetStartsFreshRound(t *testing.T) {
	var titles []string
	var streamed strings.Builder
	parser := NewArticleStreamParser(ArticleStreamParserConfig{
		OnTitle: func(title string) { titles = append(titles, title) },
		OnBody:  func(delta string) { streamed.WriteString(delta) },
	})

	parser.Push("## 中间稿\n\n中间正文")
	if parser.Protocol() != ArticleProtocolMarkdown {
		t.Fatalf("intermediate protocol = %q, want markdown", parser.Protocol())
	}
	parser.Reset()
	if parser.Protocol() != "" {
		t.Fatalf("protocol after reset = %q, want empty", parser.Protocol())
	}
	titles = nil
	streamed.Reset()
	parser.Push("## 最终稿\n\n最终正文")
	result := parser.Finalize("## 最终稿\n\n最终正文")

	if result.Title != "最终稿" || result.Body != "最终正文" || result.Protocol != ArticleProtocolMarkdown {
		t.Fatalf("unexpected final round: %+v", result)
	}
	if len(titles) != 1 || titles[0] != "最终稿" || streamed.String() != "最终正文" {
		t.Fatalf("unexpected reset emissions: titles=%v body=%q", titles, streamed.String())
	}
}
