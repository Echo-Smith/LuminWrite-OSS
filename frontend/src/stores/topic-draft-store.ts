/**
 * TopicDraftStore — 选题→写作的跨页面上下文传递
 *
 * 解决的问题：
 *   之前选题中心点击"写作"只通过 URL 传 topic.title 一个字符串，
 *   丢失了 AI 写作角度、选题描述、推荐理由等丰富上下文。
 *
 * 方案：
 *   选题中心点击"写作"时，将完整 TopicDraft 写入 store，
 *   导航到 /write 后，写作页从 store 读取 draft，
 *   自动新建会话并开始写作。
 *
 * 生命周期：
 *   draft 写入后仅消费一次（consume 后自动清空），
 *   避免刷新页面后重复触发。
 */
import { create } from "zustand";
import type { Topic, WritingAngle } from "@/lib/types";

export interface TopicDraft {
  topic: Topic;
  angle?: WritingAngle;
  recommendationReason?: string;
}

interface TopicDraftState {
  draft: TopicDraft | null;
  /** 写入 draft（选题中心 → 写作页） */
  setDraft: (draft: TopicDraft) => void;
  /** 读取并清除 draft（消费一次后自动清空） */
  consumeDraft: () => TopicDraft | null;
  /** 清除 draft（不消费） */
  clearDraft: () => void;
}

export const useTopicDraftStore = create<TopicDraftState>((set, get) => ({
  draft: null,
  setDraft: (draft) => set({ draft }),
  consumeDraft: () => {
    const d = get().draft;
    set({ draft: null });
    return d;
  },
  clearDraft: () => set({ draft: null }),
}));

/**
 * buildWritingMessage — 从 TopicDraft 组装结构化的写作指令
 *
 * 将选题上下文转化为 IntentStep 能理解的丰富指令，
 * 包含选题来源、热度、写作角度、建议字数等。
 */
export function buildWritingMessage(draft: TopicDraft): string {
  const { topic, angle } = draft;
  const parts: string[] = [];

  // 主体指令
  if (angle) {
    parts.push(`基于热搜选题「${topic.title}」，从「${angle.angle}」角度写一篇文章`);
  } else {
    parts.push(`基于热搜选题「${topic.title}」写一篇文章`);
  }

  // 选题描述（如果有）
  if (topic.description) {
    parts.push(`\n选题背景：${topic.description}`);
  }

  // 写作角度详情（如果有）
  if (angle) {
    if (angle.rationale) {
      parts.push(`写作思路：${angle.rationale}`);
    }
    if (angle.word_count > 0) {
      parts.push(`建议字数：${angle.word_count} 字`);
    }
  }

  // 推荐理由（如果有）
  if (draft.recommendationReason) {
    parts.push(`\n推荐理由：${draft.recommendationReason}`);
  }

  return parts.join("\n");
}
