package services

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/xuri/excelize/v2"
)

var testWorkbookHeaders = []string{
	"评审ID", "输出ID", "评审人", "评审角色", "评审方式", "标签来源", "是否盲评",
	"任务符合度", "来源忠实度", "结构与推理", "风格一致性", "可直接使用程度", "用户接受档位",
	"平均修改量", "硬失败ID", "主要根因", "次要根因", "评审时间", "是否仲裁", "备注",
}

func validTestWorkbookRow(reviewID string) []interface{} {
	return []interface{}{
		reviewID, "output-1", "reviewer-a", "产品经理", "人工 Excel", "holdout_v1", "是",
		4, 5, 4, 4, 4, "少量修改", "轻度", "", "", "", "2026-08-19 10:30:00", "否", "双盲评审",
	}
}

func makeReviewWorkbook(t *testing.T, headers []string, rows ...[]interface{}) *bytes.Reader {
	t.Helper()
	workbook := excelize.NewFile()
	defer workbook.Close()
	sheet := workbook.GetSheetName(0)
	for column, value := range headers {
		cell, err := excelize.CoordinatesToCellName(column+1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := workbook.SetCellValue(sheet, cell, value); err != nil {
			t.Fatal(err)
		}
	}
	for rowIndex, row := range rows {
		for column, value := range row {
			cell, err := excelize.CoordinatesToCellName(column+1, rowIndex+2)
			if err != nil {
				t.Fatal(err)
			}
			if err := workbook.SetCellValue(sheet, cell, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	buffer, err := workbook.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buffer.Bytes())
}

func TestParseWABenchReviewWorkbookAcceptsChineseContract(t *testing.T) {
	result, err := ParseWABenchReviewWorkbook(makeReviewWorkbook(t, testWorkbookHeaders, validTestWorkbookRow("review-a")))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 0 || result.ValidRows != 1 || len(result.Reviews) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	review := result.Reviews[0]
	if review.TaskCompliance != 4 || review.SourceFidelity != 5 || review.AcceptanceLabel != "light_edit" {
		t.Fatalf("canonical values not parsed: %+v", review)
	}
	if review.PrimaryRootCause != "" || review.ModificationBurden == nil || *review.ModificationBurden != 1 {
		t.Fatalf("optional values not parsed: %+v", review)
	}
	if review.Evidence["isArbitration"] != false || review.Evidence["reviewStage"] != "initial" {
		t.Fatalf("provenance evidence missing: %+v", review.Evidence)
	}
}

func TestParseWABenchReviewWorkbookReportsRowsWithoutPartialAcceptance(t *testing.T) {
	valid := validTestWorkbookRow("review-a")
	invalid := validTestWorkbookRow("review-b")
	invalid[7] = 0
	invalid[15] = "网络"
	result, err := ParseWABenchReviewWorkbook(makeReviewWorkbook(t, testWorkbookHeaders, valid, invalid))
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalRows != 2 || result.ValidRows != 1 || len(result.Errors) != 2 {
		t.Fatalf("unexpected validation summary: %+v", result)
	}
	if result.Errors[0].Row != 3 {
		t.Fatalf("error row = %d, want 3", result.Errors[0].Row)
	}
}

func TestParseWABenchReviewWorkbookRejectsMissingHeaderAndDuplicateID(t *testing.T) {
	missing := append([]string(nil), testWorkbookHeaders[:len(testWorkbookHeaders)-1]...)
	missing = missing[:7]
	result, err := ParseWABenchReviewWorkbook(makeReviewWorkbook(t, missing, validTestWorkbookRow("review-a")))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) == 0 || result.Errors[0].Row != 1 {
		t.Fatalf("missing headers were not reported: %+v", result.Errors)
	}

	result, err = ParseWABenchReviewWorkbook(makeReviewWorkbook(t, testWorkbookHeaders,
		validTestWorkbookRow("review-a"), validTestWorkbookRow("review-a")))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 1 || result.Errors[0].Row != 3 {
		t.Fatalf("duplicate review ID was not reported: %+v", result.Errors)
	}
}

func TestParseWABenchReviewWorkbookEnforcesRowLimit(t *testing.T) {
	rows := make([][]interface{}, 0, WABenchExcelMaxRows+1)
	for index := 0; index <= WABenchExcelMaxRows; index++ {
		rows = append(rows, validTestWorkbookRow(fmt.Sprintf("review-%03d", index)))
	}
	result, err := ParseWABenchReviewWorkbook(makeReviewWorkbook(t, testWorkbookHeaders, rows...))
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalRows != WABenchExcelMaxRows+1 || len(result.Errors) != 1 {
		t.Fatalf("row limit result: total=%d errors=%+v", result.TotalRows, result.Errors)
	}
}

func TestParseWABenchReviewWorkbookRejectsNonWorkbook(t *testing.T) {
	if _, err := ParseWABenchReviewWorkbook(bytes.NewBufferString("not-an-xlsx")); err == nil {
		t.Fatal("invalid workbook accepted")
	}
}

func TestBuildWABenchReviewTemplateUsesChineseContract(t *testing.T) {
	data, err := BuildWABenchReviewTemplate()
	if err != nil {
		t.Fatal(err)
	}
	workbook, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer workbook.Close()
	if got := workbook.GetSheetList(); len(got) != 2 || got[0] != "评审记录" || got[1] != "填写说明" {
		t.Fatalf("unexpected sheets: %v", got)
	}
	row, err := workbook.GetRows("评审记录")
	if err != nil {
		t.Fatal(err)
	}
	if len(row) != 1 || len(row[0]) != len(testWorkbookHeaders) {
		t.Fatalf("unexpected template shape: rows=%d columns=%d", len(row), len(row[0]))
	}
	for index, want := range testWorkbookHeaders {
		if row[0][index] != want {
			t.Fatalf("header %d = %q, want %q", index+1, row[0][index], want)
		}
	}
}
