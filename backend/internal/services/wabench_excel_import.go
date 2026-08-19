package services

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/xuri/excelize/v2"
)

const (
	WABenchExcelMaxRows      = 500
	WABenchExcelUnzipLimit   = int64(32 << 20)
	WABenchExcelXMLSizeLimit = int64(16 << 20)
)

type WABenchWorkbookError struct {
	Row     int    `json:"row"`
	Column  string `json:"column,omitempty"`
	Message string `json:"message"`
}

type WABenchWorkbookResult struct {
	SheetName string                              `json:"sheetName"`
	TotalRows int                                 `json:"totalRows"`
	ValidRows int                                 `json:"validRows"`
	Errors    []WABenchWorkbookError              `json:"errors"`
	Reviews   []database.WABenchHumanReviewImport `json:"-"`
}

var wabenchReviewHeaders = map[string][]string{
	"reviewId":            {"评审ID", "reviewId"},
	"outputId":            {"输出ID", "outputId"},
	"reviewerId":          {"评审人", "reviewerId"},
	"reviewerRole":        {"评审角色", "reviewerRole"},
	"reviewMethod":        {"评审方式", "reviewMethod"},
	"labelSource":         {"标签来源", "labelSource"},
	"isBlind":             {"是否盲评", "isBlind"},
	"taskCompliance":      {"任务符合度", "taskCompliance"},
	"sourceFidelity":      {"来源忠实度", "事实可核验性", "sourceFidelity"},
	"structureReasoning":  {"结构与推理", "结构", "structureReasoning"},
	"styleConsistency":    {"风格一致性", "styleConsistency"},
	"directUsability":     {"可直接使用程度", "directUsability"},
	"acceptanceLabel":     {"用户接受档位", "acceptanceLabel"},
	"modificationBurden":  {"平均修改量", "修改负担", "modificationBurden"},
	"hardFailureIds":      {"硬失败ID", "hardFailureIds"},
	"primaryRootCause":    {"主要根因", "primaryRootCause"},
	"secondaryRootCauses": {"次要根因", "secondaryRootCauses"},
	"reviewedAt":          {"评审时间", "reviewedAt"},
	"isArbitration":       {"是否仲裁", "isArbitration"},
	"note":                {"备注", "note"},
}

var requiredWABenchReviewColumns = []string{
	"reviewId", "outputId", "reviewerId", "reviewerRole", "reviewMethod", "labelSource",
	"isBlind", "taskCompliance", "sourceFidelity", "structureReasoning", "styleConsistency",
	"directUsability", "acceptanceLabel", "reviewedAt", "isArbitration",
}

var orderedWABenchReviewColumns = []string{
	"reviewId", "outputId", "reviewerId", "reviewerRole", "reviewMethod", "labelSource",
	"isBlind", "taskCompliance", "sourceFidelity", "structureReasoning", "styleConsistency",
	"directUsability", "acceptanceLabel", "modificationBurden", "hardFailureIds",
	"primaryRootCause", "secondaryRootCauses", "reviewedAt", "isArbitration", "note",
}

func BuildWABenchReviewTemplate() ([]byte, error) {
	workbook := excelize.NewFile()
	defer workbook.Close()
	const reviewSheet = "评审记录"
	if err := workbook.SetSheetName("Sheet1", reviewSheet); err != nil {
		return nil, err
	}
	headers := make([]interface{}, 0, len(orderedWABenchReviewColumns))
	for _, key := range orderedWABenchReviewColumns {
		headers = append(headers, wabenchReviewHeaders[key][0])
	}
	if err := workbook.SetSheetRow(reviewSheet, "A1", &headers); err != nil {
		return nil, err
	}
	headerStyle, err := workbook.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "F4F3EE"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"161917"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
	})
	if err != nil {
		return nil, err
	}
	if err := workbook.SetCellStyle(reviewSheet, "A1", "T1", headerStyle); err != nil {
		return nil, err
	}
	if err := workbook.SetRowHeight(reviewSheet, 1, 32); err != nil {
		return nil, err
	}
	if err := workbook.SetColWidth(reviewSheet, "A", "T", 18); err != nil {
		return nil, err
	}
	if err := workbook.SetColWidth(reviewSheet, "T", "T", 30); err != nil {
		return nil, err
	}
	if err := workbook.SetPanes(reviewSheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return nil, err
	}
	if err := workbook.AutoFilter(reviewSheet, "A1:T501", []excelize.AutoFilterOptions{}); err != nil {
		return nil, err
	}

	const guideSheet = "填写说明"
	if _, err := workbook.NewSheet(guideSheet); err != nil {
		return nil, err
	}
	guideRows := [][]interface{}{
		{"字段", "填写规则"},
		{"五项 Rubric", "任务符合度、来源忠实度、结构与推理、风格一致性、可直接使用程度；均填写 1—5 的整数"},
		{"用户接受档位", "直接使用 / 少量修改 / 大量修改 / 拒绝 / 未知"},
		{"平均修改量", "0 / 1 / 2 / 3，或 无需修改 / 轻度 / 中度 / 重度；未知时可留空"},
		{"主要/次要根因", "输入 / 检索 / Prompt / Memory / 工具 / 模型 / 交互；多个次要根因用中文逗号分隔"},
		{"是否盲评、是否仲裁", "是 / 否"},
		{"评审时间", "ISO 8601 或 YYYY-MM-DD HH:mm:ss"},
		{"评审ID", "每条评审必须唯一；重复 ID 不会覆盖旧记录"},
		{"输出ID", "必须引用系统中已存在的 WABench 输出 ID"},
		{"导入规则", "单次最多 500 条、8 MB；整份表全部校验通过后才会写入，失败时不会产生部分数据"},
	}
	for index, row := range guideRows {
		cell, _ := excelize.CoordinatesToCellName(1, index+1)
		if err := workbook.SetSheetRow(guideSheet, cell, &row); err != nil {
			return nil, err
		}
	}
	if err := workbook.SetCellStyle(guideSheet, "A1", "B1", headerStyle); err != nil {
		return nil, err
	}
	if err := workbook.SetColWidth(guideSheet, "A", "A", 24); err != nil {
		return nil, err
	}
	if err := workbook.SetColWidth(guideSheet, "B", "B", 90); err != nil {
		return nil, err
	}
	wrapStyle, err := workbook.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{Vertical: "top", WrapText: true}})
	if err != nil {
		return nil, err
	}
	if err := workbook.SetCellStyle(guideSheet, "A2", "B10", wrapStyle); err != nil {
		return nil, err
	}
	workbook.SetActiveSheet(0)
	var buffer bytes.Buffer
	if err := workbook.Write(&buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func normalizedWorkbookCell(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\u3000", " "))
}

func indexWABenchHeaders(row []string) map[string]int {
	result := map[string]int{}
	for index, value := range row {
		value = normalizedWorkbookCell(value)
		for canonical, aliases := range wabenchReviewHeaders {
			for _, alias := range aliases {
				if strings.EqualFold(value, alias) {
					result[canonical] = index
				}
			}
		}
	}
	return result
}

func workbookValue(row []string, headers map[string]int, key string) string {
	index, exists := headers[key]
	if !exists || index >= len(row) {
		return ""
	}
	return normalizedWorkbookCell(row[index])
}

func parseWorkbookBool(value string) (bool, error) {
	switch strings.ToLower(normalizedWorkbookCell(value)) {
	case "是", "true", "1", "yes", "y":
		return true, nil
	case "否", "false", "0", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("请填写“是”或“否”")
	}
}

func parseWorkbookScore(value, label string) (int, error) {
	score, err := strconv.Atoi(normalizedWorkbookCell(value))
	if err != nil || score < 1 || score > 5 {
		return 0, fmt.Errorf("%s必须是 1—5 的整数", label)
	}
	return score, nil
}

func parseWorkbookAcceptance(value string) (string, error) {
	labels := map[string]string{
		"直接使用": "direct_use", "direct_use": "direct_use",
		"少量修改": "light_edit", "light_edit": "light_edit",
		"大量修改": "heavy_edit", "heavy_edit": "heavy_edit",
		"拒绝": "reject", "reject": "reject",
		"未知": "unknown", "unknown": "unknown",
	}
	result, ok := labels[strings.ToLower(normalizedWorkbookCell(value))]
	if !ok {
		return "", fmt.Errorf("请使用：直接使用、少量修改、大量修改、拒绝或未知")
	}
	return result, nil
}

func parseWorkbookBurden(value string) (*int, error) {
	value = strings.ToLower(normalizedWorkbookCell(value))
	if value == "" || value == "未知" || value == "unknown" {
		return nil, nil
	}
	aliases := map[string]int{
		"0": 0, "无需修改": 0,
		"1": 1, "轻度": 1,
		"2": 2, "中度": 2,
		"3": 3, "重度": 3,
	}
	burden, ok := aliases[value]
	if !ok {
		return nil, fmt.Errorf("请填写 0—3，或无需修改/轻度/中度/重度")
	}
	return &burden, nil
}

func parseWorkbookRootCause(value string, allowEmpty bool) (string, error) {
	value = strings.ToLower(normalizedWorkbookCell(value))
	if value == "" && allowEmpty {
		return "", nil
	}
	aliases := map[string]string{
		"输入": "input", "input": "input",
		"检索": "retrieval", "retrieval": "retrieval",
		"prompt": "prompt", "提示词": "prompt",
		"memory": "memory", "记忆": "memory",
		"工具": "tool", "tool": "tool",
		"模型": "model", "model": "model",
		"交互": "interaction", "interaction": "interaction",
	}
	result, ok := aliases[value]
	if !ok {
		return "", fmt.Errorf("根因必须是：输入、检索、Prompt、Memory、工具、模型或交互")
	}
	return result, nil
}

func splitWorkbookList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == '|' || r == '\n'
	})
	result := []string{}
	seen := map[string]bool{}
	for _, part := range parts {
		part = normalizedWorkbookCell(part)
		if part != "" && !seen[part] {
			seen[part] = true
			result = append(result, part)
		}
	}
	return result
}

func parseWorkbookRoots(value string) ([]string, error) {
	result := []string{}
	for _, part := range splitWorkbookList(value) {
		root, err := parseWorkbookRootCause(part, false)
		if err != nil {
			return nil, err
		}
		result = append(result, root)
	}
	return result, nil
}

func parseWorkbookTime(value string) (time.Time, error) {
	value = normalizedWorkbookCell(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006/01/02 15:04:05", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("请使用 ISO 8601 或 YYYY-MM-DD HH:mm:ss")
}

func isWorkbookRowEmpty(row []string) bool {
	for _, value := range row {
		if normalizedWorkbookCell(value) != "" {
			return false
		}
	}
	return true
}

func parseWABenchWorkbookRow(rowNumber int, row []string, headers map[string]int) (database.WABenchHumanReviewImport, []WABenchWorkbookError) {
	result := database.WABenchHumanReviewImport{
		ReviewID:     workbookValue(row, headers, "reviewId"),
		OutputID:     workbookValue(row, headers, "outputId"),
		ReviewerID:   workbookValue(row, headers, "reviewerId"),
		ReviewerRole: workbookValue(row, headers, "reviewerRole"),
		ReviewMethod: workbookValue(row, headers, "reviewMethod"),
		LabelSource:  workbookValue(row, headers, "labelSource"),
		Evidence:     map[string]interface{}{},
	}
	errors := []WABenchWorkbookError{}
	addError := func(column, message string) {
		errors = append(errors, WABenchWorkbookError{Row: rowNumber, Column: column, Message: message})
	}
	for key, label := range map[string]string{
		"reviewId": "评审ID", "outputId": "输出ID", "reviewerId": "评审人",
		"reviewerRole": "评审角色", "reviewMethod": "评审方式", "labelSource": "标签来源",
	} {
		if workbookValue(row, headers, key) == "" {
			addError(label, "不能为空")
		}
	}
	var err error
	if result.IsBlind, err = parseWorkbookBool(workbookValue(row, headers, "isBlind")); err != nil {
		addError("是否盲评", err.Error())
	}
	if result.TaskCompliance, err = parseWorkbookScore(workbookValue(row, headers, "taskCompliance"), "任务符合度"); err != nil {
		addError("任务符合度", err.Error())
	}
	if result.SourceFidelity, err = parseWorkbookScore(workbookValue(row, headers, "sourceFidelity"), "来源忠实度"); err != nil {
		addError("来源忠实度", err.Error())
	}
	if result.StructureReasoning, err = parseWorkbookScore(workbookValue(row, headers, "structureReasoning"), "结构与推理"); err != nil {
		addError("结构与推理", err.Error())
	}
	if result.StyleConsistency, err = parseWorkbookScore(workbookValue(row, headers, "styleConsistency"), "风格一致性"); err != nil {
		addError("风格一致性", err.Error())
	}
	if result.DirectUsability, err = parseWorkbookScore(workbookValue(row, headers, "directUsability"), "可直接使用程度"); err != nil {
		addError("可直接使用程度", err.Error())
	}
	if result.AcceptanceLabel, err = parseWorkbookAcceptance(workbookValue(row, headers, "acceptanceLabel")); err != nil {
		addError("用户接受档位", err.Error())
	}
	if result.ModificationBurden, err = parseWorkbookBurden(workbookValue(row, headers, "modificationBurden")); err != nil {
		addError("平均修改量", err.Error())
	}
	result.HardFailureIDs = splitWorkbookList(workbookValue(row, headers, "hardFailureIds"))
	if result.PrimaryRootCause, err = parseWorkbookRootCause(workbookValue(row, headers, "primaryRootCause"), true); err != nil {
		addError("主要根因", err.Error())
	}
	if result.SecondaryRootCauses, err = parseWorkbookRoots(workbookValue(row, headers, "secondaryRootCauses")); err != nil {
		addError("次要根因", err.Error())
	}
	if result.ReviewedAt, err = parseWorkbookTime(workbookValue(row, headers, "reviewedAt")); err != nil {
		addError("评审时间", err.Error())
	}
	isArbitration, err := parseWorkbookBool(workbookValue(row, headers, "isArbitration"))
	if err != nil {
		addError("是否仲裁", err.Error())
	}
	result.Evidence["isArbitration"] = isArbitration
	if isArbitration {
		result.Evidence["reviewStage"] = "arbitration"
	} else {
		result.Evidence["reviewStage"] = "initial"
	}
	if note := workbookValue(row, headers, "note"); note != "" {
		result.Evidence["note"] = note
	}
	return result, errors
}

func ParseWABenchReviewWorkbook(reader io.Reader) (*WABenchWorkbookResult, error) {
	workbook, err := excelize.OpenReader(reader, excelize.Options{
		RawCellValue:      true,
		UnzipSizeLimit:    WABenchExcelUnzipLimit,
		UnzipXMLSizeLimit: WABenchExcelXMLSizeLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("无法读取 Excel 文件: %w", err)
	}
	defer workbook.Close()
	sheets := workbook.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("Excel 中没有工作表")
	}
	result := &WABenchWorkbookResult{SheetName: sheets[0], Errors: []WABenchWorkbookError{}, Reviews: []database.WABenchHumanReviewImport{}}
	rows, err := workbook.Rows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("无法读取工作表 %s: %w", sheets[0], err)
	}
	defer rows.Close()
	rowNumber := 0
	headers := map[string]int{}
	seenReviewIDs := map[string]int{}
	for rows.Next() {
		rowNumber++
		columns, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("读取第 %d 行失败: %w", rowNumber, err)
		}
		if rowNumber == 1 {
			headers = indexWABenchHeaders(columns)
			for _, column := range requiredWABenchReviewColumns {
				if _, exists := headers[column]; !exists {
					result.Errors = append(result.Errors, WABenchWorkbookError{Row: 1, Column: wabenchReviewHeaders[column][0], Message: "缺少必填列"})
				}
			}
			if len(result.Errors) > 0 {
				return result, nil
			}
			continue
		}
		if isWorkbookRowEmpty(columns) {
			continue
		}
		result.TotalRows++
		if result.TotalRows > WABenchExcelMaxRows {
			result.Errors = append(result.Errors, WABenchWorkbookError{Row: rowNumber, Message: fmt.Sprintf("单次最多导入 %d 条评审", WABenchExcelMaxRows)})
			break
		}
		review, rowErrors := parseWABenchWorkbookRow(rowNumber, columns, headers)
		if firstRow, exists := seenReviewIDs[review.ReviewID]; review.ReviewID != "" && exists {
			rowErrors = append(rowErrors, WABenchWorkbookError{Row: rowNumber, Column: "评审ID", Message: fmt.Sprintf("与第 %d 行重复", firstRow)})
		} else if review.ReviewID != "" {
			seenReviewIDs[review.ReviewID] = rowNumber
		}
		if len(rowErrors) > 0 {
			result.Errors = append(result.Errors, rowErrors...)
			continue
		}
		result.Reviews = append(result.Reviews, review)
		result.ValidRows++
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("关闭工作表失败: %w", err)
	}
	if err := rows.Error(); err != nil {
		return nil, fmt.Errorf("读取工作表失败: %w", err)
	}
	if result.TotalRows == 0 && len(result.Errors) == 0 {
		result.Errors = append(result.Errors, WABenchWorkbookError{Row: 2, Message: "评审表没有数据行"})
	}
	return result, nil
}
