export interface HarnessChange {
  sessionId: string;
  entryCount: number;
  lastKind?: string;
}

/**
 * 业务代码只依赖这个口。一个 Pi 对话属于一个会话，不属于文档。
 * 子 Agent 以后作为这个对话上的 task 子对话，不另挂到文档上。
 * pi-durable 1.3 的真实形状（submit 挂在 Conversation 上、watch 是回调、resume 同步）
 * 被包在实现里，不泄漏到外面。
 */
export interface HarnessPort {
  submit(
    sessionId: string,
    input: { content: string; requestId: string; whenBusy?: "steer" | "followUp" | "reject" },
  ): Promise<{ submissionId: string }>;
  abort(sessionId: string): Promise<void>;
  resume(): Promise<void>;
  watch(sessionId: string): AsyncIterable<HarnessChange>;
  close(): Promise<void>;
}
