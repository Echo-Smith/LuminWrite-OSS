export interface HarnessChange {
  conversationKey: string;
  entryCount: number;
  lastKind?: string;
}

/**
 * 业务代码只依赖这个口。pi-durable 1.3 的真实形状（submit 挂在 Conversation 上、
 * watch 是回调、resume 同步）被包在实现里，不泄漏到外面。
 */
export interface HarnessPort {
  submit(
    conversationKey: string,
    input: { content: string; requestId: string; whenBusy?: "steer" | "followUp" | "reject" },
  ): Promise<{ submissionId: string }>;
  abort(conversationKey: string): Promise<void>;
  resume(): Promise<void>;
  watch(conversationKey: string): AsyncIterable<HarnessChange>;
  close(): Promise<void>;
}
