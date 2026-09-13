package memoryport

// 渲染函数自 internal/engine/steps 的 FormatMemoryForPrompt /
// FormatReviewGuardForPrompt 原样搬迁（输出逐字节一致，P0 黄金快照锁定）。
// 格式化归属契约侧（计划 §4.2 纪律 2）：预算与拼装仍归消费方。

// RenderWriteDirectives 将偏好指令渲染为写作/对话 prompt 片段。
func RenderWriteDirectives(b *Bundle) string {
	if b == nil || len(b.WriteDirectives) == 0 {
		return ""
	}

	var sb []byte
	sb = append(sb, "\n\n--- 用户写作偏好（请参考但不强制）---\n"...)
	for _, d := range b.WriteDirectives {
		sb = append(sb, "- "...)
		sb = append(sb, d.Value...)
		sb = append(sb, '\n')
	}
	return string(sb)
}

// RenderReviewGuard 将反馈指令渲染为审校标准片段。
func RenderReviewGuard(b *Bundle) string {
	if b == nil || len(b.ReviewGuard) == 0 {
		return ""
	}

	var sb []byte
	sb = append(sb, "\n\n--- 用户历史反馈（审查时请检查）---\n"...)
	for _, d := range b.ReviewGuard {
		sb = append(sb, "- "...)
		sb = append(sb, d.Value...)
		sb = append(sb, '\n')
	}
	return string(sb)
}
