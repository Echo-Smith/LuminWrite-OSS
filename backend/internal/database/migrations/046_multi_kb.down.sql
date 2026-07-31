-- 046 down: 回滚多知识库
DROP TABLE IF EXISTS knowledge_bases;
ALTER TABLE knowledge_chunks DROP COLUMN IF EXISTS kb_id;
UPDATE knowledge_base SET kb_id = NULL;
