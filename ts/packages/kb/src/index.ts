export { chunkText, type TextChunk } from "./chunk.js";
export {
  createFakeEmbedder,
  createOpenAiEmbedder,
  EMBED_DIM,
  type EmbeddingPort,
  FAKE_EMBED_MODEL,
} from "./embed.js";
export {
  type CitationView,
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
  "知识库地基：上传、jieba 分块、全文与向量混合检索。已保存文档按最新版本索引；未保存、已丢弃或移出知识库的不进检索。真实嵌入只走 kb.embed 槽上的 BYOK 凭据；没有凭据时只做全文。引用是否对得上来源由 research 校验。";
