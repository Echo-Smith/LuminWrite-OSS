# 无信息删除测试（逐句）

下面给出全文和其中一句。请判断：**删除这一句后，读者会丢失什么信息？**

全文：
{{TEXT}}

待测句：{{SENTENCE}}

只输出 JSON：{"loses":"nothing|restatement_only|new_fact|new_reason|new_example|transition_needed","explain":"一句话"}
"nothing"与"restatement_only"计为可删句。
