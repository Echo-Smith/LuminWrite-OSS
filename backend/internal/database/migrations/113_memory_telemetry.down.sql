-- 回滚 116：删除遥测表（只追加数据，无回填语义）。

DROP TABLE IF EXISTS memory_telemetry;
