# 原子论断抽取（沿用 AR-012 口径）

把文本拆为**原子论断**：每条只含一个可核查的事实/数字/因果/归因。去重；明显标为推测、观点或作者判断的不计入（标 type=opinion）。

输出 JSON：{"claims":[{"id":"","sentence_idx":0,"text":"","type":"fact|number|causal|attribution|opinion","cited_sources":["S3"]}]}

文本：
{{TEXT}}
