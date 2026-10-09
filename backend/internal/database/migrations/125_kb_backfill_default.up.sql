-- 125: Backfill kb_id for documents created after 046.
-- 125: Backfill kb_id for documents created after 046.
--
-- Migration 046 seeded the 'default' KB and backfilled kb_id once, but
-- KbManager.AddDocument (the materials / RSS-ingest / file-parse write
-- path) kept inserting rows with kb_id NULL, so those documents were
-- invisible to the KB views that filter by kb_id (ListDocumentsInKB).
-- AddDocument now writes 'default' at insert time; this migration repairs
-- the rows created in between. Chunks inherit kb_id from their parent
-- document, so the chunk backfill mirrors 046's shape.
--
-- 迁移 046 曾一次性把存量 kb_id 刷成 'default'，但此后 AddDocument（素材 /
-- RSS 入库 / 文件解析的写入路径）插入的行一直是 kb_id NULL，这些文档在按
-- kb_id 过滤的知识库视图（ListDocumentsInKB）里查不到。AddDocument 现已
-- 在插入时写 'default'，本迁移修复这段窗口期内产生的行。chunk 的 kb_id
-- 从父文档继承，回滚形状与 046 相同。

UPDATE knowledge_base SET kb_id = 'default' WHERE kb_id IS NULL OR kb_id = '';

UPDATE knowledge_chunks kc
SET kb_id = COALESCE((SELECT kb_id FROM knowledge_base kb WHERE kb.id = kc.doc_id), 'default')
WHERE kc.kb_id IS NULL OR kc.kb_id = '';
