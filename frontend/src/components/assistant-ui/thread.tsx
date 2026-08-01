/**
 * Thread 组件 — 消息流容器（自动滚动 + 空状态）
 *
 * 使用动画体系：
 *   - 空状态：品牌渐变图标 + 淡入上移 + stagger 建议按钮
 *   - 消息列表：自动平滑滚动
 */
import { useRef, useEffect, useCallback, useState } from "react";
import { PenLine, Lightbulb, Sparkles, ArrowDown } from "lucide-react";
import { UserMessage } from "./user-message";
import { AssistantMessage } from "./assistant-message";
import { useAgentStore } from "@/stores/agent-store";
import { Button } from "@/components/ui/button";
import { FadeIn, StaggerItem } from "@/components/animation";

export function Thread() {
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
    return (
      <div className="flex h-full items-center justify-center p-8">
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
          <FadeIn direction="up" delay={200} className="space-y-3">
            <p className="text-xs text-muted-foreground font-mono-sm">试试这些</p>
            <div className="flex flex-wrap justify-center gap-2">
              {[
                "基于热搜写一篇关于外卖骑手闯红灯的评论",
                "写一篇关于城市垃圾分类的政论文章",
                "帮我润色一段关于社区治理的文字",
              ].map((suggestion, i) => (
                <StaggerItem
                  key={suggestion}
                  index={i}
                  interval={80}
                  animation="fade-up"
                  as="div"
                >
                  <SuggestionButton text={suggestion} />
                </StaggerItem>
              ))}
            </div>
          </FadeIn>
        </div>
      </div>
    );
  }

  return (
    <div className="relative h-full">
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="h-full overflow-y-auto"
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

function SuggestionButton({ text }: { text: string }) {
  const startWriting = useAgentStore((s) => s.startWriting);

  return (
    <Button
      variant="outline"
      size="sm"
      className="gap-1.5 text-xs "
      onClick={() => startWriting({ message: text })}
    >
      <Lightbulb className="h-3 w-3 text-amber-500" />
      {text}
    </Button>
  );
}
