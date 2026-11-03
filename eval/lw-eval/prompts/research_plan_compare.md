# 检索规划评审：与专家研究计划比对

任务：{{TASK}}
专家研究计划（金标准子问题列表）：{{GOLD_PLAN}}
Agent 生成的研究计划/检索查询：{{AGENT_PLAN}}

对每个金标准子问题，判断 Agent 计划中是否有**实质等价**的子问题或查询（同义改写算覆盖；更宽泛的总括问题不算）。
再列出 Agent 计划中的"无效"子问题（与任务无关、重复、无法检索）。

输出 JSON：{"coverage":{"G1":"covered|partial|missed",...},"invalid":["A3"],"missing_critical":["最重要的遗漏，最多3条"]}
