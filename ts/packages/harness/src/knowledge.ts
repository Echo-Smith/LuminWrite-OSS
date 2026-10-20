export interface KnowledgeHit {
  sourceId: string;
  chunkId: string;
  title: string;
  kind: string;
  text: string;
  score: number;
  charStart: number;
  charEnd: number;
}

/** 写作回合里的只读知识库。所有者由会话推出，不从模型参数来。 */
export interface SessionKnowledge {
  search(input: {
    sessionId: string;
    query: string;
    limit?: number;
    kinds?: string[];
    recencyDays?: number;
    library?: boolean;
  }): Promise<{ note: string; hits: KnowledgeHit[] }>;
  cite?(input: {
    sessionId: string;
    sourceId: string;
    quote: string;
    turnId: string;
    questionId?: string;
    planId?: string;
    blockId?: string;
  }): Promise<{
    evidenceId: string;
    citationStatus: string;
    note?: string;
    citedVersionId: string | null;
    sourceHasNewerVersion: boolean;
  }>;
  readSource(input: {
    sessionId: string;
    sourceId: string;
    charStart?: number;
    charEnd?: number;
  }): Promise<{
    note: string;
    title: string;
    body: string;
    revision: number;
    kind: string;
    citedVersionId?: string;
  }>;
}
