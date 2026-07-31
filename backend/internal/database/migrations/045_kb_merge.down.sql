-- 045 down: 回滚 WeKnora 合并 migration

-- 删除新增表
DROP TABLE IF EXISTS kb_configs;
DROP TABLE IF EXISTS kb_relations;
DROP TABLE IF EXISTS kb_entities;
DROP TABLE IF EXISTS knowledge_chunks;

-- 回滚 user_materials 扩展列
ALTER TABLE user_materials
    DROP COLUMN IF EXISTS doc_id,
    DROP COLUMN IF EXISTS chunk_count;

-- 回滚 knowledge_base 扩展列
ALTER TABLE knowledge_base
    DROP COLUMN IF EXISTS user_id,
    DROP COLUMN IF EXISTS kb_id,
    DROP COLUMN IF EXISTS source_type,
    DROP COLUMN IF EXISTS source_url,
    DROP COLUMN IF EXISTS file_name,
    DROP COLUMN IF EXISTS file_size,
    DROP COLUMN IF EXISTS chunk_count,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS parent_id;

-- 注意：不卸载 pg_bm25 扩展，因为可能有其他对象依赖
