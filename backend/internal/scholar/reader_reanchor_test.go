package scholar

import (
	"strings"
	"testing"
)

func TestReanchorQuoteRecoversConstantOffsetDrift(t *testing.T) {
	// 真实案例形状：模型把所有 offsets 整体 -1（或 +1），quote 本身逐字出现。
	text := "αβγ深度研究试点材料：quarterly review of regional energy adoption, 2026.后续文字"
	runes := []rune(text)
	quote := "quarterly review of regional energy adoption"
	trueStart := strings.Index(string(text), "quarterly") // rune index 与 byte index 不同，直接算
	// 计算 rune 下标
	trueStart = len([]rune(text[:strings.Index(text, "quarterly")]))
	trueEnd := trueStart + len([]rune(quote))

	re, ok := reanchorQuote(runes, quote, MaxQuoteCodepoints)
	if !ok {
		t.Fatal("verbatim quote must be re-anchorable")
	}
	if re[0] != int64(trueStart) || re[1] != int64(trueEnd) {
		t.Fatalf("re-anchored [%d:%d), want [%d:%d)", re[0], re[1], trueStart, trueEnd)
	}
	// 不变量：重锚定后 offsets 必须精确定位 quote
	if string(runes[re[0]:re[1]]) != quote {
		t.Fatal("re-anchored slice does not equal quote")
	}
	// 模型给的错误 offsets 必然不匹配（这正是触发重锚定的前提）
	if string(runes[trueStart-1:trueEnd-1]) == quote {
		t.Fatal("test premise broken: drift offsets unexpectedly match")
	}
}

func TestReanchorQuoteRejectsNonVerbatimQuote(t *testing.T) {
	runes := []rune("区块链与分布式账本技术在供应链金融中的应用研究")
	if _, ok := reanchorQuote(runes, "分布式账本在供应链中的应用", MaxQuoteCodepoints); ok {
		t.Fatal("paraphrased quote must not be re-anchorable")
	}
	if _, ok := reanchorQuote(runes, "", MaxQuoteCodepoints); ok {
		t.Fatal("empty quote must not be re-anchorable")
	}
}

func TestReanchorQuoteFirstOccurrenceWins(t *testing.T) {
	text := "同一段重复同一段重复结尾"
	runes := []rune(text)
	re, ok := reanchorQuote(runes, "同一段", MaxQuoteCodepoints)
	if !ok || re[0] != 0 || re[1] != 3 {
		t.Fatalf("first occurrence expected, got [%d:%d) ok=%v", re[0], re[1], ok)
	}
}
