export interface ConversationSessionRef {
  conversationId?: string | null;
  traceId?: string | null;
}

// A trace identifies one agent run; a conversation identifies the chain of
// turns. The first trace remains the backend conversation key across follow-ups.
export function resolveConversationId(
  explicitSessionId: string | undefined,
  session?: ConversationSessionRef,
): string | undefined {
  return explicitSessionId ?? session?.conversationId ?? session?.traceId ?? undefined;
}
