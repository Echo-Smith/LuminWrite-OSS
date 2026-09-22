-- 110: User-editable session title.
--
-- task_name 由服务端 LLM 自动提取、article_title 由写作生成，两者都会覆盖
-- 用户自己的命名。新增 custom_title 作为用户显式命名的最高优先级来源。

ALTER TABLE agent_traces
    ADD COLUMN IF NOT EXISTS custom_title VARCHAR(128);
