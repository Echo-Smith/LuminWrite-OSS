export { chunkText, type TextChunk } from "./chunk.js";
export {
  createFakeEmbedder,
  createOpenAiEmbedder,
  EMBED_DIM,
  type EmbeddingPort,
  FAKE_EMBED_MODEL,
} from "./embed.js";
export {
  createKnowledgeBase,
  type KbHit,
  type KnowledgeBase,
  retrievalDataNote,
  type SourceKind,
} from "./kb.js";
export { KbInputError, normalizeBody, parseUpload } from "./parse.js";
export { RETRIEVAL_POOL, RRF_K, rrfMerge } from "./rrf.js";
export { searchText, segment } from "./segment.js";

export const phase = "foundation" as const;
export const note =
  "知识库地基：上传、jieba 分块、全文与向量混合检索。未保存或已丢弃的文档不进检索（documentVisibleToKb）。真实嵌入只走 kb.embed 槽上的 BYOK 凭据；没有凭据时只做全文。";
