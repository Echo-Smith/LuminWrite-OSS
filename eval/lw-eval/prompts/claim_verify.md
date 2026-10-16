# 异源论断核查（须与生成模型不同供应商）

只依据下面提供的来源片段判断，不得使用你自己的知识。

论断：{{CLAIM}}
文中引用的来源：{{CITED}}
来源包全文片段：{{SOURCES}}

输出 JSON：{"verdict":"supported|partial|unsupported|contradicted","best_source":"S?","quote":"来源原文中的支撑句（逐字）","overreach":"none|scope|causal|number|consensus"}
规则：quote 必须能在来源中逐字找到，否则 verdict 不得为 supported。
