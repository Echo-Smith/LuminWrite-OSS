-- 回退 128_memory_history_unique_relax。
-- 注意：若回退前已产生同维度多行 superseded 历史或同维度多行
-- superseded 与活记忆并存的数据，回添 uk_user_memory 会因违反约束
-- 失败——需先将多余历史行归档（archived）再执行本 down。
DROP INDEX IF EXISTS uk_user_memory_live;

ALTER TABLE user_memories
    ADD CONSTRAINT uk_user_memory UNIQUE (user_id, category, key, status);
