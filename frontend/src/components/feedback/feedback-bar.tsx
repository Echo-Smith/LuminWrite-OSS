/**
 * 反馈面板 — 写作完成后的分段评分
 *
 * 支持：
 *  - 标题评分（1-5 星）
 *  - 各段落评分（1-5 星）
 *  - 整体评价（好/一般/差 + 文字评论）
 *  - 通过 WebSocket 提交到后端 POST /api/v2/feedback
 *
 * 文档来源: docs/07-feedback.md
 */
import { useState, useMemo, useCallback } from "react";
import { Star, ThumbsUp, ThumbsDown, Minus, Send, CheckCircle2, ChevronDown, ChevronUp } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { useAgentStore } from "@/stores/agent-store";
import type { FeedbackType, FeedbackSegment } from "@/lib/types";
import { cn } from "@/lib/utils";

interface FeedbackBarProps {
  traceId: string;
  article: string;
}

// ─── 工具函数 ──────────────────────────────────────────────

/** 从 Markdown 文章中提取标题和段落 */
function parseArticleSegments(article: string): { title: string | null; paragraphs: string[] } {
  const lines = article.split("\n");
  let title: string | null = null;
  const paragraphs: string[] = [];
  let currentPara: string[] = [];

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) {
      if (currentPara.length > 0) {
        paragraphs.push(currentPara.join(" "));
        currentPara = [];
      }
      continue;
    }

    // Markdown 标题 (## or #)
    if (trimmed.startsWith("## ") || trimmed.startsWith("# ")) {
      if (title === null) {
        title = trimmed.replace(/^#+\s*/, "");
        continue;
      }
    }

    // Skip separator lines
    if (trimmed.startsWith("---")) {
      if (currentPara.length > 0) {
        paragraphs.push(currentPara.join(" "));
        currentPara = [];
      }
      continue;
    }

    currentPara.push(trimmed);
  }

  if (currentPara.length > 0) {
    paragraphs.push(currentPara.join(" "));
  }

  return { title, paragraphs };
}

// ─── 星级评分组件 ──────────────────────────────────────────

function StarRating({
  value,
  onChange,
  disabled,
}: {
  value: number;
  onChange: (v: number) => void;
  disabled?: boolean;
}) {
  const [hover, setHover] = useState(0);

  return (
    <div className="flex items-center gap-0.5">
      {[1, 2, 3, 4, 5].map((star) => (
        <button
          key={star}
          disabled={disabled}
          onClick={() => onChange(star)}
          onMouseEnter={() => setHover(star)}
          onMouseLeave={() => setHover(0)}
          className="transition-transform hover:scale-110 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Star
            className={cn(
              "h-4 w-4",
              star <= (hover || value)
                ? "fill-amber-400 text-amber-400"
                : "fill-transparent text-muted-foreground/30"
            )}
          />
        </button>
      ))}
    </div>
  );
}

// ─── 主组件 ────────────────────────────────────────────────

export function FeedbackBar({ traceId, article }: FeedbackBarProps) {
  const sendWS = useAgentStore((s) => s.sendWS);

  const { title, paragraphs } = useMemo(() => parseArticleSegments(article), [article]);

  // 各段评分状态
  const [titleRating, setTitleRating] = useState(0);
  const [paragraphRatings, setParagraphRatings] = useState<Record<number, number>>({});
  const [overallRating, setOverallRating] = useState<FeedbackType | null>(null);
  const [comment, setComment] = useState("");
  const [expanded, setExpanded] = useState(false);
  const [submitted, setSubmitted] = useState(false);

  const hasSegmentFeedback = titleRating > 0 || Object.values(paragraphRatings).some((v) => v > 0);

  const handleSubmit = useCallback(() => {
    const segments: FeedbackSegment[] = [];

    // 标题评分
    if (title && titleRating > 0) {
      segments.push({
        segment_type: "title",
        segment_index: 0,
        segment_text: title,
        rating: titleRating,
        feedback_type: titleRating >= 4 ? "good" : titleRating >= 3 ? "suggestion" : "bad",
        comment: "",
      });
    }

    // 段落评分
    for (const [idxStr, rating] of Object.entries(paragraphRatings)) {
      if (rating <= 0) continue;
      const idx = parseInt(idxStr);
      const text = paragraphs[idx] ?? "";
      segments.push({
        segment_type: "paragraph",
        segment_index: idx,
        segment_text: text.slice(0, 200),
        rating,
        feedback_type: rating >= 4 ? "good" : rating >= 3 ? "suggestion" : "bad",
        comment: "",
      });
    }

    // 整体评价
    if (overallRating) {
      const overallScore = overallRating === "good" ? 5 : overallRating === "suggestion" ? 3 : 1;
      segments.push({
        segment_type: "overall",
        segment_index: 0,
        segment_text: article.slice(0, 200),
        rating: overallScore,
        feedback_type: overallRating,
        comment,
      });
    }

    if (segments.length === 0) return;

    // 通过 WebSocket 提交
    sendWS("feedback.submit", { trace_id: traceId, segments });

    // 同时通过 REST API 提交（确保落库）
    fetch("/api/v2/feedback", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ trace_id: traceId, segments }),
    }).catch(() => {
      // WS 已发送，REST 失败可忽略
    });

    setSubmitted(true);
  }, [title, titleRating, paragraphs, paragraphRatings, overallRating, comment, article, traceId, sendWS]);

  // ─── 快捷整体评价（不展开分段） ──────────────────────────
  const handleQuickFeedback = (type: FeedbackType) => {
    setOverallRating(type);
    if (type === "suggestion" || type === "bad") {
      setExpanded(true);
    } else {
      // 好评直接提交
      const segments: FeedbackSegment[] = [
        {
          segment_type: "overall",
          segment_index: 0,
          segment_text: article.slice(0, 200),
          rating: 5,
          feedback_type: "good",
          comment: "",
        },
      ];
      sendWS("feedback.submit", { trace_id: traceId, segments });
      fetch("/api/v2/feedback", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ trace_id: traceId, segments }),
      }).catch(() => {});
      setSubmitted(true);
    }
  };

  if (submitted) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground py-2">
        <CheckCircle2 className="h-4 w-4 text-green-600" />
        <span>感谢反馈！你的评价已提交。</span>
      </div>
    );
  }

  return (
    <div className="rounded-lg border bg-background p-3 space-y-3">
      {/* 标题行 */}
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium">文章评价</span>
        <div className="flex items-center gap-1.5">
          {/* 快捷按钮 */}
          <Button
            size="sm"
            variant="outline"
            className={cn("h-7 gap-1 border-green-300 text-green-700 hover:bg-green-50", overallRating === "good" && "bg-green-50")}
            onClick={() => handleQuickFeedback("good")}
          >
            <ThumbsUp className="h-3 w-3" />
            好
          </Button>
          <Button
            size="sm"
            variant="outline"
            className={cn("h-7 gap-1 border-amber-300 text-amber-700 hover:bg-amber-50", overallRating === "suggestion" && "bg-amber-50")}
            onClick={() => handleQuickFeedback("suggestion")}
          >
            <Minus className="h-3 w-3" />
            一般
          </Button>
          <Button
            size="sm"
            variant="outline"
            className={cn("h-7 gap-1 border-red-300 text-red-700 hover:bg-red-50", overallRating === "bad" && "bg-red-50")}
            onClick={() => handleQuickFeedback("bad")}
          >
            <ThumbsDown className="h-3 w-3" />
            差
          </Button>

          {/* 展开分段评分 */}
          <Button
            size="sm"
            variant="ghost"
            className="h-7 gap-1"
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
            分段评分
          </Button>
        </div>
      </div>

      {/* 分段评分区域 */}
      {expanded && (
        <div className="space-y-2.5 border-t pt-2.5">
          {/* 标题评分 */}
          {title && (
            <div className="flex items-center justify-between gap-3">
              <div className="flex items-center gap-2 min-w-0">
                <Badge variant="outline" className="shrink-0 text-xs">标题</Badge>
                <span className="truncate text-xs text-muted-foreground">{title}</span>
              </div>
              <StarRating value={titleRating} onChange={setTitleRating} />
            </div>
          )}

          {/* 段落评分 */}
          {paragraphs.map((para, i) => (
            <div key={i} className="flex items-center justify-between gap-3">
              <div className="flex items-center gap-2 min-w-0">
                <Badge variant="outline" className="shrink-0 text-xs">P{i + 1}</Badge>
                <span className="truncate text-xs text-muted-foreground">
                  {para.slice(0, 60)}...
                </span>
              </div>
              <StarRating
                value={paragraphRatings[i] ?? 0}
                onChange={(v) => setParagraphRatings((prev) => ({ ...prev, [i]: v }))}
              />
            </div>
          ))}

          {/* 整体评论 */}
          {(overallRating === "suggestion" || overallRating === "bad" || hasSegmentFeedback) && (
            <div className="space-y-1.5">
              <Textarea
                value={comment}
                onChange={(e) => setComment(e.target.value)}
                placeholder="补充说明（可选）..."
                className="h-16 text-xs"
                rows={2}
              />
            </div>
          )}

          {/* 提交按钮 */}
          {hasSegmentFeedback && (
            <Button
              size="sm"
              onClick={handleSubmit}
              className="w-full gap-1.5"
            >
              <Send className="h-3.5 w-3.5" />
              提交分段评分
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
