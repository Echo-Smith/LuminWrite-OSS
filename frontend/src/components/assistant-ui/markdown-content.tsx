/**
 * Markdown 渲染组件 — 使用 react-markdown 直接渲染
 *
 * 支持：
 * - GFM（表格、删除线、任务列表等）
 * - 原始 HTML（<sup>脚注引用</sup> 等）
 * - 论文引用文献格式（[1], [2] 角标 + 参考文献列表）
 */
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeRaw from "rehype-raw";
import type { Components } from "react-markdown";

const components: Components = {
  // 脚注/引用上标 <sup>
  sup: ({ children }) => (
    <sup className="text-[0.7em] font-semibold text-primary align-super">
      {children}
    </sup>
  ),
  // 下标 <sub>
  sub: ({ children }) => (
    <sub className="text-[0.7em] text-muted-foreground align-sub">
      {children}
    </sub>
  ),
  // 链接 — 新窗口打开
  a: ({ href, children }) => (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="text-primary underline underline-offset-2 hover:text-primary/80 transition-ui"
    >
      {children}
    </a>
  ),
  // 定义列表（术语表）<dl>
  dl: ({ children }) => (
    <dl className="my-3 space-y-1">{children}</dl>
  ),
  dt: ({ children }) => (
    <dt className="font-semibold text-sm mt-2">{children}</dt>
  ),
  dd: ({ children }) => (
    <dd className="text-sm text-muted-foreground pl-4 border-l-2 border-border">{children}</dd>
  ),
};

export function MarkdownContent({ content }: { content: string }) {
  return (
    <div className="prose-article max-w-none text-sm leading-relaxed">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeRaw]}
        components={components}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
