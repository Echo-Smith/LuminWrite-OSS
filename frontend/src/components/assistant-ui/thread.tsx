/**
 * Thread 组件 — 消息流容器（自动滚动 + 空状态）
 *
 * 使用动画体系：
 *   - 空状态：品牌渐变图标 + 淡入上移 + stagger 建议按钮
 *   - 消息列表：自动平滑滚动
 *
 * 空状态建议按钮接入热搜 API：
 *   - 进入空状态时请求 /api/v2/topics?filter=hot&page_size=5，取 Top 3
 *   - 点击建议时携带 topic_url 等完整 Topic 上下文（后端可据此抓取事件背景）
 *   - 请求失败或为空时 fallback 到硬编码建议
 */
import { useRef, useEffect, useCallback, useState } from "react";
import { PenLine, Lightbulb, Sparkles, ArrowDown, Flame, Loader2 } from "lucide-react";
import { UserMessage } from "./user-message";
import { AssistantMessage } from "./assistant-message";
import { useAgentStore } from "@/stores/agent-store";
import { FadeIn, StaggerItem } from "@/components/animation";
import type { Topic, AgentStartPayload } from "@/lib/types";

export function Thread({ variant = "full" }: { variant?: "full" | "dock" }) {
  const sessions = useAgentStore((s) => s.sessions);
  const activeSessionId = useAgentStore((s) => s.activeSessionId);
  const streamingText = useAgentStore((s) => s.streamingText);

  const session = sessions.find((s) => s.id === activeSessionId);
  const messages = session?.messages ?? [];
  const scrollRef = useRef<HTMLDivElement>(null);
  const isAtBottomRef = useRef(true);
  const [showScrollBtn, setShowScrollBtn] = useState(false);

  // 检测用户是否在底部附近（允许小幅偏移）
  const handleScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const threshold = 80; // px — 认为在底部的容差
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
    isAtBottomRef.current = atBottom;
    setShowScrollBtn(!atBottom);
  }, []);

  const scrollToBottom = useCallback(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
      isAtBottomRef.current = true;
      setShowScrollBtn(false);
    }
  }, []);

  // 仅当用户已在底部时才自动滚动
  useEffect(() => {
    if (scrollRef.current && isAtBottomRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages, streamingText]);

  // 新消息时（非流式更新），如果用户在底部则滚动
  useEffect(() => {
    if (scrollRef.current && isAtBottomRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages.length]);

  // 空状态
  if (messages.length === 0) {
    return <EmptyState compact={variant === "dock"} />;
  }

  return (
    <div className="relative h-full">
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className={variant === "dock" ? "h-full overflow-y-auto px-2" : "h-full overflow-y-auto"}
      >
      {messages.map((message, idx) => {
        // 计算文章版本号：统计到当前位置为止有多少条带文本内容的 assistant 消息
        let articleVersion = 0;
        let totalArticleVersions = 0;
        for (let i = 0; i < messages.length; i++) {
          const msg = messages[i];
          if (msg.role !== "assistant") continue;
          const hasArticleText = msg.parts.some(
            (p) => p.type === "text" && (p as { text: string }).text.trim().length > 0
          );
          if (hasArticleText) {
            totalArticleVersions++;
            if (i === idx) articleVersion = totalArticleVersions;
          }
        }
        // 只对有文章内容的消息计算版本
        const hasText = message.parts.some(
          (p) => p.type === "text" && (p as { text: string }).text.trim().length > 0
        );

        return message.role === "user" ? (
          <UserMessage key={message.id} message={message} />
        ) : (
          <AssistantMessage
            key={message.id}
            message={message}
            traceId={session?.traceId ?? null}
            version={hasText ? articleVersion : undefined}
            totalVersions={hasText && totalArticleVersions > 1 ? totalArticleVersions : undefined}
          />
        );
      })}
        {/* 底部间距 */}
        <div className="h-4" />
      </div>
      {/* 滚动到底部按钮 */}
      {showScrollBtn && (
        <button
          onClick={scrollToBottom}
          className="absolute bottom-4 left-1/2 -translate-x-1/2 z-10 flex h-9 w-9 items-center justify-center rounded-full border border-border/60 bg-background/90 backdrop-blur-sm shadow-md text-muted-foreground hover:text-foreground hover:bg-accent transition-ui anim-fade-in"
          title="滚动到最新"
        >
          <ArrowDown className="h-4 w-4" />
          {streamingText && (
            <span className="absolute -top-1 -right-1 flex h-2.5 w-2.5">
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary opacity-60" />
              <span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-primary" />
            </span>
          )}
        </button>
      )}
    </div>
  );
}

// ─── 空状态组件 ───────────────────────────────────────────

/** 硬编码 fallback 建议（热搜 API 失败时使用） */
const FALLBACK_SUGGESTIONS = [
  "写一篇关于城市垃圾分类的政论文章",
  "帮我润色一段关于社区治理的文字",
  "基于热搜写一篇关于外卖骑手闯红灯的评论",
];

/**
 * 从热搜话题生成写作建议文案
 * 格式：基于热搜「话题标题」写一篇评论
 * 截断长标题以适配按钮宽度
 */
function buildSuggestionText(topic: Topic): string {
  const title = topic.title.length > 24 ? `${topic.title.slice(0, 24)}…` : topic.title;
  return `基于热搜「${title}」写一篇评论`;
}

/**
 * 将 Topic 转换为 AgentStartPayload
 * 携带 topic_url（后端可据此抓取事件背景增强写作）
 * 并将选题描述注入 user_materials
 */
function topicToPayload(topic: Topic): AgentStartPayload {
  const userMaterials: string[] = [];
  if (topic.description) {
    userMaterials.push(`📎 ${topic.title}: ${topic.description}`);
  }

  return {
    message: `基于热搜选题「${topic.title}」写一篇评论文章${topic.url ? `\n热搜来源：${topic.url}` : ""}`,
    mode: "writing",
    topic_url: topic.url || undefined,
    user_materials: userMaterials.length > 0 ? userMaterials : undefined,
  };
}

/** 单个建议按钮 — 豆包风格：全宽、细边框、圆角 */
function SuggestionButton({
  text,
  payload,
  isHot = false,
}: {
  text: string;
  payload?: AgentStartPayload;
  isHot?: boolean;
}) {
  const startWriting = useAgentStore((s) => s.startWriting);

  return (
    <button
      onClick={() => startWriting(payload ?? { message: text })}
      className="flex w-full items-center gap-1.5 h-[42px] rounded-xl border border-border/5 px-3 text-left text-sm text-foreground transition-ui hover:bg-accent/60"
    >
      {isHot ? (
        <Flame className="h-4 w-4 shrink-0 text-orange-500" />
      ) : (
        <Lightbulb className="h-4 w-4 shrink-0 text-amber-500" />
      )}
      <span className="truncate">{text}</span>
    </button>
  );
}

function EmptyState({ compact = false }: { compact?: boolean }) {
  const [suggestions, setSuggestions] = useState<{ text: string; payload?: AgentStartPayload; isHot?: boolean }[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;

    (async () => {
      try {
        const res = await fetch("/api/v2/topics?filter=hot&page=1&page_size=5");
        const json = await res.json();
        const data = json.data ?? json;
        const topics = (data?.topics ?? []) as Topic[];

        if (cancelled) return;

        if (topics.length > 0) {
          const topThree = topics.slice(0, 3);
          setSuggestions(
            topThree.map((topic) => ({
              text: buildSuggestionText(topic),
              payload: topicToPayload(topic),
              isHot: true,
            })),
          );
        } else {
          // 没有热搜数据 → fallback
          setSuggestions(
            FALLBACK_SUGGESTIONS.map((text) => ({ text })),
          );
        }
      } catch {
        if (cancelled) return;
        setSuggestions(FALLBACK_SUGGESTIONS.map((text) => ({ text })));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => { cancelled = true; };
  }, []);

  return (
    <div className={compact ? "flex h-full items-center justify-center px-5 py-3" : "flex h-full items-center justify-center p-8"}>
      {compact ? (
        <div className="w-full max-w-2xl text-center">
          <p className="text-sm font-medium">从一句写作要求开始</p>
          <p className="mt-1 text-xs text-muted-foreground">系统会先整理合约与计划，正文始终留在上方文档区。</p>
        </div>
      ) : (
      <div className="max-w-md text-center space-y-8">
        {/* 品牌图标 */}
        <FadeIn direction="scale" className="flex justify-center">
          <div className="relative flex h-20 w-20 items-center justify-center rounded-3xl bg-brand-gradient shadow-lg">
            <PenLine className="h-9 w-9 text-white" />
            <div className="absolute -top-1 -right-1 flex h-6 w-6 items-center justify-center rounded-full bg-amber-400 shadow-sm">
              <Sparkles className="h-3.5 w-3.5 text-white" />
            </div>
          </div>
        </FadeIn>

        {/* 标题 */}
        <FadeIn direction="up" delay={100} className="space-y-2">
          <h2 className="text-2xl font-bold tracking-tight">笔润智谈写作工作台</h2>
          <p className="text-sm text-muted-foreground leading-relaxed">
            输入你的写作需求，AI 会自动检索素材、生成提纲、撰写文章并进行质量自检。
          </p>
        </FadeIn>

        {/* 建议按钮 */}
        <FadeIn direction="up" delay={200} className="space-y-3 w-full max-w-lg">
          <div className="flex items-center justify-center gap-1.5">
            <Flame className="h-3.5 w-3.5 text-orange-500" />
            <p className="text-[12px] text-muted-foreground/60">
              {loading ? "正在获取热搜话题…" : suggestions.some((s) => s.isHot) ? "试试这些热搜话题" : "试试这些"}
            </p>
            {loading && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />}
          </div>
          <div className="flex flex-col items-center gap-2.5">
            {loading ? (
              // 骨架占位
              [0, 1, 2].map((i) => (
                <div
                  key={i}
                  className="h-[42px] w-full max-w-md animate-pulse rounded-xl bg-muted/60"
                />
              ))
            ) : (
              suggestions.map((suggestion, i) => (
                <StaggerItem
                  key={suggestion.text}
                  index={i}
                  interval={80}
                  animation="fade-up"
                  as="div"
                  className="w-full max-w-md"
                >
                  <SuggestionButton
                    text={suggestion.text}
                    payload={suggestion.payload}
                    isHot={suggestion.isHot}
                  />
                </StaggerItem>
              ))
            )}
          </div>
        </FadeIn>
      </div>
      )}
    </div>
  );
}
