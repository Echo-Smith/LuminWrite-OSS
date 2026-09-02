package projectmemory

import (
	"strings"
	"testing"
)

func TestValidateTerminologyNormalizesAndRejectsCollisions(t *testing.T) {
	entry := Terminology{TerminologyID: "term_test", ProjectID: "prj_test", Term: " 生成式检索 ",
		Aliases: []string{"GSR", "gsr "}, Forbidden: []string{"AI搜索", "AI 搜索"}, SourceRunID: "run_test"}
	if err := ValidateTerminology(&entry); err != nil {
		t.Fatalf("err=%v", err)
	}
	// Trim + case-fold dedupe; interior spacing is preserved, so "AI搜索"
	// and "AI 搜索" stay distinct forbidden spellings.
	if entry.Term != "生成式检索" || len(entry.Aliases) != 1 || entry.Aliases[0] != "GSR" || len(entry.Forbidden) != 2 {
		t.Fatalf("normalized=%#v", entry)
	}
	entry.Forbidden = append(entry.Forbidden, "gsr")
	if err := ValidateTerminology(&entry); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("collision err=%v", err)
	}
	entry.SourceRunID, entry.Forbidden = "", nil
	if err := ValidateTerminology(&entry); err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("provenance err=%v", err)
	}
}

func TestValidateCuratedBasics(t *testing.T) {
	decision := Decision{DecisionID: "dec_test", ProjectID: "prj_test", Statement: " 全文用“模型” ",
		SourceRefs: []string{"doc_store"}}
	if err := ValidateDecision(&decision); err != nil {
		t.Fatalf("decision err=%v", err)
	}
	if decision.Statement != "全文用“模型”" {
		t.Fatalf("statement=%q", decision.Statement)
	}
	decision.Statement = "  "
	if err := ValidateDecision(&decision); err == nil || !strings.Contains(err.Error(), "statement is required") {
		t.Fatalf("blank statement err=%v", err)
	}

	question := OpenQuestion{QuestionID: "qu_test", ProjectID: "prj_test", Question: " 数据口径以哪家年报为准？ ",
		SourceRunID: "run_test"}
	if err := ValidateOpenQuestion(&question); err != nil {
		t.Fatalf("question err=%v", err)
	}
	if question.Question != "数据口径以哪家年报为准？" {
		t.Fatalf("question=%q", question.Question)
	}

	thread := Thread{ThreadID: "thr_test", ProjectID: "prj_test", Label: " 论点链：检索优于重排 ",
		Resident: false, SourceRefs: []string{"doc_store"}}
	if err := ValidateThread(&thread); err != nil {
		t.Fatalf("thread err=%v", err)
	}
	if thread.Label != "论点链：检索优于重排" || thread.Resident {
		t.Fatalf("thread=%#v", thread)
	}
}
