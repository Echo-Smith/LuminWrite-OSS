package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestWABenchCenterLimit(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/?limit=250", nil)
	limit, err := wabenchCenterLimit(request)
	if err != nil || limit != 250 {
		t.Fatalf("limit=%d err=%v", limit, err)
	}
	for _, value := range []string{"0", "501", "invalid"} {
		request = httptest.NewRequest(http.MethodGet, "/?limit="+value, nil)
		if _, err := wabenchCenterLimit(request); err == nil {
			t.Fatalf("invalid limit %q accepted", value)
		}
	}
}

func TestWABenchReviewTemplateHandler(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Server{}).handleAdminWABenchReviewTemplate(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	result := recorder.Result()
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", result.StatusCode, recorder.Body.String())
	}
	if got := result.Header.Get("Content-Disposition"); got != `attachment; filename="wabench-review-template-zh.xlsx"` {
		t.Fatalf("content disposition=%q", got)
	}
	workbook, err := excelize.OpenReader(bytes.NewReader(recorder.Body.Bytes()))
	if err != nil {
		t.Fatalf("response is not an xlsx workbook: %v", err)
	}
	defer workbook.Close()
	if got := workbook.GetSheetList(); len(got) != 2 || got[0] != "评审记录" {
		t.Fatalf("unexpected sheets: %v", got)
	}
}
