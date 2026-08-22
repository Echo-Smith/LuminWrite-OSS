/**
 * 文章导出工具 — 支持 Markdown / Word (docx) / PDF 格式
 *
 * 设计原则：
 *   - 纯前端实现，无需后端 API
 *   - Word 使用 application/vnd.openxmlformats-officedocument.wordprocessingml.document MIME + HTML 格式（兼容 Word 和 WPS）
 *   - PDF 使用浏览器原生 print + 专用打印窗口
 *   - 导出 Word/PDF 前清除 Markdown 原始符号（##、**、^、* 等）
 *   - 无需额外 npm 依赖
 */

/**
 * 清除 Markdown 原始符号，保留纯文本内容
 *
 * 处理内容：
 *   - 行首标题前缀 ## # ### → 移除前缀保留文本
 *   - 粗体 **text** → text
 *   - 斜体 *text* → text
 *   - 删除线 ~~text~~ → text
 *   - 上标 ^text^ → text
 *   - 行内代码 `text` → text
 *   - 引用前缀 > → 移除
 *   - 列表标记 - / * / \d+. → 移除
 *   - 分隔线 --- / *** → 移除
 *   - 表格管道符 | → 保留文本，移除管道
 *   - HTML 标签 <sup>...</sup> 等 → 保留内容，移除标签
 */
function stripMarkdownSymbols(markdown: string): string {
  let text = markdown;

  // 1. 先处理代码块（整块提取，避免内部被清洗）
  const codeBlocks: string[] = [];
  text = text.replace(/```(\w*)\n([\s\S]*?)```/g, (_m, _lang, code) => {
    const placeholder = `__CODEBLOCK_${codeBlocks.length}__`;
    codeBlocks.push(code.trim());
    return placeholder;
  });

  // 2. 行内代码 → 提取纯文本
  text = text.replace(/`([^`]+)`/g, "$1");

  // 3. 标题前缀 ## / ### / # → 移除前缀，保留文本
  text = text.replace(/^#{1,6}\s+/gm, "");

  // 4. 粗体 **text** → text
  text = text.replace(/\*\*([^*]+)\*\*/g, "$1");

  // 5. 斜体 *text* → text（注意在粗体之后处理）
  text = text.replace(/\*([^*]+)\*/g, "$1");

  // 6. 删除线 ~~text~~ → text
  text = text.replace(/~~([^~]+)~~/g, "$1");

  // 7. 上标 ^text^ → text（Markdown 扩展语法的上标）
  text = text.replace(/\^([^\^\n]+)\^/g, "$1");

  // 8. HTML 标签 <sup>...</sup> / <sub>...</sub> → 保留内容，移除标签
  text = text.replace(/<sup[^>]*>([\s\S]*?)<\/sup>/gi, "$1");
  text = text.replace(/<sub[^>]*>([\s\S]*?)<\/sub>/gi, "$1");
  text = text.replace(/<[^>]+>/g, ""); // 移除其他残留 HTML 标签

  // 9. 引用前缀 > → 移除
  text = text.replace(/^>\s+/gm, "");

  // 10. 无序列表标记 - / * → 移除（行首）
  text = text.replace(/^[-*]\s+/gm, "");

  // 11. 有序列表标记 1. → 移除（行首）
  text = text.replace(/^\d+\.\s+/gm, "");

  // 12. 分隔线 --- / *** → 移除整行
  text = text.replace(/^(---+|\*\*\*+)\s*$/gm, "");

  // 13. 表格管道符 | → 移除管道，保留文本
  text = text.replace(/^\|/gm, "");
  text = text.replace(/\|$/gm, "");
  text = text.replace(/\|/g, "  ");
  // 表格分隔行（---:---）移除
  text = text.replace(/^[\s:|-]+$/gm, "");

  // 14. 恢复代码块
  codeBlocks.forEach((block, i) => {
    text = text.replace(`__CODEBLOCK_${i}__`, block);
  });

  // 15. 清理多余空行
  text = text.replace(/\n{3,}/g, "\n\n");

  return text.trim();
}

/**
 * 简单 Markdown → HTML 转换器
 * 不依赖 React 渲染，纯字符串处理
 */
function markdownToHtml(markdown: string): string {
  let html = markdown;

  // 转义 HTML 特殊字符（保留已处理的标签）
  // 先处理代码块（避免被其他规则影响）
  const codeBlocks: string[] = [];
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (_m, _lang, code) => {
    const placeholder = `__CODEBLOCK_${codeBlocks.length}__`;
    const escaped = code.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    codeBlocks.push(`<pre><code>${escaped.trim()}</code></pre>`);
    return placeholder;
  });

  // 行内代码
  const inlineCodes: string[] = [];
  html = html.replace(/`([^`]+)`/g, (_m, code) => {
    const placeholder = `__INLINECODE_${inlineCodes.length}__`;
    const escaped = code.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    inlineCodes.push(`<code>${escaped}</code>`);
    return placeholder;
  });

  // 标题（h1-h3）
  html = html.replace(/^### (.+)$/gm, "<h3>$1</h3>");
  html = html.replace(/^## (.+)$/gm, "<h2>$1</h2>");
  html = html.replace(/^# (.+)$/gm, "<h1>$1</h1>");

  // 粗体和斜体
  html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  html = html.replace(/\*([^*]+)\*/g, "<em>$1</em>");

  // 引用
  html = html.replace(/^> (.+)$/gm, "<blockquote>$1</blockquote>");

  // 无序列表
  html = html.replace(/^[-*] (.+)$/gm, "<li>$1</li>");
  html = html.replace(/(<li>.*<\/li>\n?)+/g, (m) => `<ul>${m}</ul>`);

  // 有序列表
  html = html.replace(/^\d+\. (.+)$/gm, "<li>$1</li>");

  // 表格（简单处理）
  if (/\|/.test(html)) {
    html = html.replace(/^\|(.+)\|$/gm, (m) => {
      const cells = m.split("|").filter((c) => c.trim());
      const isSeparator = cells.every((c) => /^[\s-:]+$/.test(c));
      if (isSeparator) return "";
      const tag = cells[0]?.startsWith("---") ? "th" : "td";
      const row = cells.map((c) => `<${tag}>${c.trim()}</${tag}>`).join("");
      return `<tr>${row}</tr>`;
    });
    html = html.replace(/(<tr>[\s\S]*?<\/tr>\n?)+/g, (m) => `<table>${m}</table>`);
  }

  // 段落（连续非标签行）
  html = html.replace(/^(?!<[a-z])((?!<[a-z])[^\n]+)$/gm, "<p>$1</p>");

  // 合并空行
  html = html.replace(/\n{2,}/g, "\n");

  // 恢复代码块
  codeBlocks.forEach((block, i) => {
    html = html.replace(`__CODEBLOCK_${i}__`, block);
  });
  inlineCodes.forEach((code, i) => {
    html = html.replace(`__INLINECODE_${i}__`, code);
  });

  return html;
}

/**
 * 获取安全的文件名
 */
function safeFileName(title?: string): string {
  const name = (title ?? "article").slice(0, 30).replace(/[<>:"/\\|?*\n]/g, "_");
  return name || "article";
}

/**
 * 导出为 Markdown 文件
 */
export function exportMarkdown(text: string, title?: string) {
  const fullText = title ? `# ${title}\n\n${text}` : text;
  const blob = new Blob([fullText], { type: "text/markdown;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${safeFileName(title)}.md`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

/**
 * 导出为 Word (.docx) 文件
 * 使用 HTML 格式 + application/vnd.openxmlformats-officedocument.wordprocessingml.document MIME
 * 导出前清除 Markdown 原始符号（##、**、^ 等）
 */
export function exportWord(text: string, title?: string) {
  const cleanText = stripMarkdownSymbols(text);
  const bodyHtml = markdownToHtml(cleanText);
  const fullTitle = title ?? "未命名文章";

  // 构建 Word 兼容的 HTML 文档
  const html = `<!DOCTYPE html>
<html xmlns:o="urn:schemas-microsoft-com:office:office"
      xmlns:w="urn:schemas-microsoft-com:office:word"
      xmlns="http://www.w3.org/TR/REC-html40">
<head>
<meta charset="utf-8">
<title>${fullTitle}</title>
<!--[if gte mso 9]>
<xml>
<w:WordDocument>
<w:View>Print</w:View>
<w:Zoom>100</w:Zoom>
<w:DoNotOptimizeForBrowser/>
</w:WordDocument>
</xml>
<![endif]-->
<style>
@page { size: A4; margin: 2.54cm; }
body { font-family: "宋体", "SimSun", "Times New Roman", serif; font-size: 12pt; line-height: 1.8; color: #333; }
h1 { font-size: 18pt; text-align: center; margin-bottom: 20pt; font-weight: bold; }
h2 { font-size: 16pt; margin-top: 16pt; margin-bottom: 8pt; font-weight: bold; }
h3 { font-size: 14pt; margin-top: 12pt; margin-bottom: 6pt; font-weight: bold; }
p { text-indent: 2em; margin: 0 0 8pt 0; text-align: justify; }
blockquote { border-left: 3pt solid #ccc; padding-left: 12pt; margin-left: 0; color: #666; font-style: italic; }
code { font-family: "Courier New", monospace; font-size: 10.5pt; background: #f5f5f5; padding: 1pt 3pt; }
pre { font-family: "Courier New", monospace; font-size: 10.5pt; background: #f5f5f5; padding: 8pt; overflow: auto; }
table { border-collapse: collapse; width: 100%; margin: 8pt 0; }
td, th { border: 1pt solid #ccc; padding: 4pt 8pt; }
th { background: #f0f0f0; font-weight: bold; }
</style>
</head>
<body>
<h1>${fullTitle}</h1>
${bodyHtml}
</body>
</html>`;

  const blob = new Blob(["\ufeff", html], { type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${safeFileName(title)}.docx`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

/**
 * 导出为 PDF（通过浏览器打印）
 * 打开一个新窗口，写入格式化 HTML，触发 print
 */
export function exportPDF(text: string, title?: string) {
  const cleanText = stripMarkdownSymbols(text);
  const bodyHtml = markdownToHtml(cleanText);
  const fullTitle = title ?? "未命名文章";

  const html = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>${fullTitle}</title>
<style>
@page { size: A4; margin: 2.54cm; }
* { box-sizing: border-box; }
body {
  font-family: "Songti SC", "宋体", "SimSun", "Times New Roman", serif;
  font-size: 12pt;
  line-height: 1.8;
  color: #333;
  max-width: 680px;
  margin: 0 auto;
  padding: 20px;
}
h1 {
  font-size: 20pt;
  text-align: center;
  margin-bottom: 24pt;
  font-weight: bold;
}
h2 { font-size: 16pt; margin-top: 20pt; margin-bottom: 10pt; font-weight: bold; }
h3 { font-size: 14pt; margin-top: 16pt; margin-bottom: 8pt; font-weight: bold; }
p { text-indent: 2em; margin: 0 0 10pt 0; text-align: justify; }
blockquote { border-left: 3px solid #ccc; padding-left: 16px; margin-left: 0; color: #666; font-style: italic; }
code { font-family: "Menlo", "Courier New", monospace; font-size: 10.5pt; background: #f5f5f5; padding: 2px 4px; border-radius: 3px; }
pre { font-family: "Menlo", "Courier New", monospace; font-size: 10.5pt; background: #f5f5f5; padding: 10px; overflow: auto; border-radius: 4px; }
table { border-collapse: collapse; width: 100%; margin: 10pt 0; }
td, th { border: 1px solid #ccc; padding: 6pt 10pt; text-align: left; }
th { background: #f0f0f0; font-weight: bold; }
@media print {
  body { max-width: none; padding: 0; }
}
</style>
</head>
<body>
<h1>${fullTitle}</h1>
${bodyHtml}
<script>
window.onload = function() {
  setTimeout(function() {
    window.print();
  }, 300);
};
</script>
</body>
</html>`;

  const printWindow = window.open("", "_blank");
  if (!printWindow) {
    alert("请允许弹出窗口以导出 PDF");
    return;
  }
  printWindow.document.write(html);
  printWindow.document.close();
}
