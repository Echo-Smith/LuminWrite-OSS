package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

const (
	wabenchExcelMaxFileSize = int64(8 << 20)
	wabenchMultipartMaxSize = int64(10 << 20)
)

func (s *Server) requireWABenchRepo(w http.ResponseWriter) bool {
	if s.wabenchRepo != nil {
		return true
	}
	response.Err(w, http.StatusServiceUnavailable, "wabench_unavailable", "WABench repository is unavailable")
	return false
}

func wabenchCenterLimit(r *http.Request) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get("limit"))
	if value == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 500 {
		return 0, fmt.Errorf("limit must be between 1 and 500")
	}
	return limit, nil
}

func parseWABenchCenterLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit, err := wabenchCenterLimit(r)
	if err != nil {
		response.Err(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return 0, false
	}
	return limit, true
}

func (s *Server) handleAdminWABenchOverview(w http.ResponseWriter, r *http.Request) {
	if !s.requireWABenchRepo(w) {
		return
	}
	result, err := s.wabenchRepo.GetCenterOverview(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "wabench_overview_failed", "Unable to load WABench overview")
		return
	}
	response.OK(w, result)
}

func (s *Server) handleAdminWABenchSuites(w http.ResponseWriter, r *http.Request) {
	if !s.requireWABenchRepo(w) {
		return
	}
	limit, ok := parseWABenchCenterLimit(w, r)
	if !ok {
		return
	}
	items, err := s.wabenchRepo.ListCenterSuites(r.Context(), limit)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "wabench_suites_failed", "Unable to load WABench suites")
		return
	}
	response.OK(w, map[string]interface{}{"items": items, "count": len(items)})
}

func (s *Server) handleAdminWABenchCandidates(w http.ResponseWriter, r *http.Request) {
	if !s.requireWABenchRepo(w) {
		return
	}
	limit, ok := parseWABenchCenterLimit(w, r)
	if !ok {
		return
	}
	items, err := s.wabenchRepo.ListCenterCandidates(r.Context(), limit)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "wabench_candidates_failed", "Unable to load WABench candidates")
		return
	}
	response.OK(w, map[string]interface{}{"items": items, "count": len(items)})
}

func (s *Server) handleAdminWABenchRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requireWABenchRepo(w) {
		return
	}
	limit, ok := parseWABenchCenterLimit(w, r)
	if !ok {
		return
	}
	items, err := s.wabenchRepo.ListCenterRuns(r.Context(), limit)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "wabench_runs_failed", "Unable to load WABench runs")
		return
	}
	response.OK(w, map[string]interface{}{"items": items, "count": len(items)})
}

func (s *Server) handleAdminWABenchReviews(w http.ResponseWriter, r *http.Request) {
	if !s.requireWABenchRepo(w) {
		return
	}
	limit, ok := parseWABenchCenterLimit(w, r)
	if !ok {
		return
	}
	items, err := s.wabenchRepo.ListCenterReviews(r.Context(), r.URL.Query().Get("runId"), limit)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "wabench_reviews_failed", "Unable to load WABench reviews")
		return
	}
	response.OK(w, map[string]interface{}{"items": items, "count": len(items)})
}

func (s *Server) handleAdminWABenchBadcases(w http.ResponseWriter, r *http.Request) {
	if !s.requireWABenchRepo(w) {
		return
	}
	limit, ok := parseWABenchCenterLimit(w, r)
	if !ok {
		return
	}
	items, err := s.wabenchRepo.ListCenterBadcases(r.Context(), limit)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "wabench_badcases_failed", "Unable to load WABench Badcases")
		return
	}
	response.OK(w, map[string]interface{}{"items": items, "count": len(items)})
}

func (s *Server) handleAdminWABenchReleases(w http.ResponseWriter, r *http.Request) {
	if !s.requireWABenchRepo(w) {
		return
	}
	limit, ok := parseWABenchCenterLimit(w, r)
	if !ok {
		return
	}
	items, err := s.wabenchRepo.ListCenterReleases(r.Context(), limit)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "wabench_releases_failed", "Unable to load WABench releases")
		return
	}
	response.OK(w, map[string]interface{}{"items": items, "count": len(items)})
}

func writeWABenchWorkbookValidation(w http.ResponseWriter, result *services.WABenchWorkbookResult) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"data": map[string]interface{}{
			"sheetName": result.SheetName,
			"totalRows": result.TotalRows,
			"validRows": result.ValidRows,
			"errors":    result.Errors,
		},
		"error": map[string]string{
			"code":    "invalid_review_workbook",
			"message": "Excel 评审表未通过校验，未写入任何记录",
		},
	})
}

func openWABenchReviewUpload(w http.ResponseWriter, r *http.Request) (multipart.File, *multipart.FileHeader, error) {
	r.Body = http.MaxBytesReader(w, r.Body, wabenchMultipartMaxSize)
	if err := r.ParseMultipartForm(wabenchMultipartMaxSize); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return nil, nil, fmt.Errorf("Excel 文件超过 8 MB 限制")
		}
		return nil, nil, fmt.Errorf("无法读取上传表单")
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, nil, fmt.Errorf("请选择 Excel 评审文件")
	}
	if strings.ToLower(filepath.Ext(header.Filename)) != ".xlsx" {
		file.Close()
		return nil, nil, fmt.Errorf("仅支持 .xlsx 文件")
	}
	if header.Size <= 0 || header.Size > wabenchExcelMaxFileSize {
		file.Close()
		return nil, nil, fmt.Errorf("Excel 文件必须小于 8 MB")
	}
	return file, header, nil
}

func (s *Server) handleAdminImportWABenchReviews(w http.ResponseWriter, r *http.Request) {
	if !s.requireWABenchRepo(w) {
		return
	}
	file, header, err := openWABenchReviewUpload(w, r)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		response.Err(w, http.StatusBadRequest, "invalid_review_upload", err.Error())
		return
	}
	defer file.Close()
	result, err := services.ParseWABenchReviewWorkbook(file)
	if err != nil {
		response.Err(w, http.StatusUnprocessableEntity, "invalid_review_workbook", err.Error())
		return
	}
	if len(result.Errors) > 0 {
		writeWABenchWorkbookValidation(w, result)
		return
	}
	if err := s.wabenchRepo.InsertHumanReviews(r.Context(), result.Reviews); err != nil {
		response.Err(w, http.StatusConflict, "review_import_conflict", err.Error())
		return
	}
	response.Created(w, map[string]interface{}{
		"fileName":     header.Filename,
		"sheetName":    result.SheetName,
		"importedRows": result.ValidRows,
	})
}

func (s *Server) handleAdminWABenchReviewTemplate(w http.ResponseWriter, _ *http.Request) {
	data, err := services.BuildWABenchReviewTemplate()
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "review_template_failed", "Unable to create WABench review template")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="wabench-review-template-zh.xlsx"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
