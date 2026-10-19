# 论证图抽取（逻辑性评测第 1 步）

把下文拆成论证图。只抽取文中**明确写出**的内容，不要补全作者没写的推理。

输出 JSON：
{
 "thesis": "全文中心论点（原文引用或忠实概括）",
 "claims": [{"id":"C1","text":"","para":段落序号,"type":"main|sub|fact|recommendation"}],
 "supports": [{"from":"C3","to":"C1","kind":"evidence|reason|example","warrant_stated":true|false}],
 "attacks": [{"from":"","to":"","addressed":true|false}],
 "orphans": ["没有任何支持、也不支持任何上级论点的 claim id"],
 "leaps": [{"to":"C2","why":"从前提到结论缺少的一步"}]
}

文本：
{{TEXT}}
