/**
 * StreamText — 流式文字逐词浮现动画
 *
 * 参考 transitions.dev 的 streaming text 效果：
 * 将容器内的文本节点按词组（中文 2-3 字一组，英文按空格分词）
 * 包裹在 .t-stream-w span 中，然后逐词添加 .is-in 类触发动画。
 *
 * 用法：
 *   <StreamText streaming={isStreaming}>
 *     <MarkdownContent content={text} />
 *   </StreamText>
 *
 * 当 streaming=true 时，每次内容变化都会对新出现的文本做逐词动画。
 * 当 streaming=false（已完成）时，所有文本直接可见。
 */
import { useRef, useEffect, type ReactNode } from "react";

interface StreamTextProps {
  /** 是否正在流式输出 */
  streaming: boolean;
  /** 子内容 */
  children: ReactNode;
}

/**
 * 将文本按词组拆分。
 * 中文：每 2 个字一组（兼顾阅读节奏和动画密度）
 * 英文/数字：按空格分词，保留标点
 * 换行符：保留为独立组
 */
function splitText(text: string): string[] {
  if (!text) return [];
  const tokens: string[] = [];
  let i = 0;
  while (i < text.length) {
    const char = text[i];

    // 换行符单独一组
    if (char === "\n") {
      tokens.push("\n");
      i++;
      continue;
    }

    // 空格合并
    if (char === " " || char === "\t") {
      let spaces = "";
      while (i < text.length && (text[i] === " " || text[i] === "\t")) {
        spaces += text[i];
        i++;
      }
      tokens.push(spaces);
      continue;
    }

    // 中日韩统一表意文字 + 全角标点 → 2 字一组
    const isCJK = /[\u4e00-\u9fff\u3400-\u4dbf\uf900-\ufaff\u3000-\u303f\uff00-\uffef]/.test(char);
    if (isCJK) {
      let group = "";
      let count = 0;
      while (i < text.length && /[\u4e00-\u9fff\u3400-\u4dbf\uf900-\ufaff\u3000-\u303f\uff00-\uffef]/.test(text[i]) && count < 2) {
        group += text[i];
        i++;
        count++;
      }
      tokens.push(group);
      continue;
    }

    // 英文/数字/半角标点 → 按空格分词
    let word = "";
    while (i < text.length && !/[\u4e00-\u9fff\u3400-\u4dbf\uf900-\ufaff\u3000-\u303f\uff00-\uffef\n \t]/.test(text[i])) {
      word += text[i];
      i++;
    }
    if (word) tokens.push(word);
  }
  return tokens;
}

/**
 * 判断是否为可动画的文本节点（非空白）
 */
function hasAnimatableText(text: string): boolean {
  return text.length > 0 && text.trim().length > 0;
}

/**
 * 将文本节点替换为按词包裹的 span
 * 返回创建的 span 元素列表（需要添加 .is-in 的）
 */
function wrapTextNode(textNode: Text): HTMLElement[] {
  const text = textNode.textContent;
  if (!text || !hasAnimatableText(text)) return [];

  const tokens = splitText(text);
  if (tokens.length === 0) return [];

  const frag = document.createDocumentFragment();
  const spans: HTMLElement[] = [];

  for (const token of tokens) {
    if (token === "\n") {
      frag.appendChild(document.createElement("br"));
      continue;
    }
    if (!token.trim()) {
      // 纯空白直接添加，不包裹
      frag.appendChild(document.createTextNode(token));
      continue;
    }
    const span = document.createElement("span");
    span.className = "t-stream-w";
    span.textContent = token;
    frag.appendChild(span);
    spans.push(span);
  }

  textNode.parentNode?.replaceChild(frag, textNode);
  return spans;
}

/**
 * 遍历容器内所有文本节点，将未处理的文本节点包裹为 span
 * 跳过已包含 .t-stream-w 的元素
 */
function processContainer(container: HTMLElement): HTMLElement[] {
  const newSpans: HTMLElement[] = [];

  // 收集所有未处理的文本节点
  const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const parent = node.parentElement;
      if (!parent) return NodeFilter.FILTER_REJECT;
      // 跳过已包裹在 t-stream-w 中的
      if (parent.classList.contains("t-stream-w")) return NodeFilter.FILTER_REJECT;
      // 跳过 script/style/textarea
      const tag = parent.tagName.toLowerCase();
      if (tag === "script" || tag === "style" || tag === "textarea" || tag === "input") return NodeFilter.FILTER_REJECT;
      // 跳过 code 块中的文本（代码不做动画）
      if (parent.closest("pre") || parent.closest("code")) return NodeFilter.FILTER_REJECT;
      if (!node.textContent || !hasAnimatableText(node.textContent)) return NodeFilter.FILTER_REJECT;
      return NodeFilter.FILTER_ACCEPT;
    },
  });

  const textNodes: Text[] = [];
  let node: Node | null;
  while ((node = walker.nextNode())) {
    textNodes.push(node as Text);
  }

  for (const textNode of textNodes) {
    newSpans.push(...wrapTextNode(textNode));
  }

  return newSpans;
}

/**
 * 逐词添加 .is-in 类
 */
function revealSpans(spans: HTMLElement[], startIndex: number = 0) {
  if (spans.length === 0) return;
  const gap = parseInt(getComputedStyle(document.documentElement).getPropertyValue("--stream-gap")) || 50;

  for (let i = 0; i < spans.length; i++) {
    const span = spans[startIndex + i];
    if (!span) continue;
    // 已经显示的跳过
    if (span.classList.contains("is-in")) continue;
    setTimeout(() => {
      span.classList.add("is-in");
    }, i * gap);
  }
}

export function StreamText({ streaming, children }: StreamTextProps) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    if (!streaming) {
      // 非流式模式：确保所有 .t-stream-w 都可见
      const spans = container.querySelectorAll(".t-stream-w:not(.is-in)");
      spans.forEach((s) => s.classList.add("is-in"));
      return;
    }

    // 流式模式：用 rAF 确保 DOM 已更新后处理新增文本节点
    const raf = requestAnimationFrame(() => {
      const newSpans = processContainer(container);
      if (newSpans.length > 0) {
        revealSpans(newSpans, 0);
      }
    });
    return () => cancelAnimationFrame(raf);
  }, [streaming, children]);

  return <div ref={containerRef}>{children}</div>;
}
