# 记忆抽取对齐：belief 上下文进抽取 prompt（WP7）

**日期**：2026-10-13
**范围**：OSS 先行，商业线受控移植。UserMemory（user_memories）抽取链的偏好演化（preference evolution）对齐修复。

## 背景

偏好演化此前完全依赖**事后字符串对齐**：抽取器输出封闭词表的 key，`ConflictResolver.ResolveAndSave` 按 `category+key` 精确匹配 + value 字符串相等判定"强化 / 撤回 / 新建"。两个方向相反的失分机制同根：

1. **漏撤回（key 漂移）**：抽取器输出 `prefers_short_title` 而非 `title_style` 时查不到旧记忆，走新建——旧 belief 依然 active，矛盾偏好并存注入。
2. **误撤回 + 候选降级（措辞漂移）**：同 key 但 value 措辞略变（"理性克制" vs "理性、克制"）被判为冲突——已确立的 active 偏好被撤回，替代者从 candidate 起步等 two-strike，偏好"演化"表现为震荡。

## 变更

1. **`pkg/memory/types.go`**：`ExtractedMemory` 新增可选 `action`（`reinforce` | `retract`）与 `retract_target`（prompt 序号 1..k）；新增 `BeliefContext`（Index/ID/Category/Key/Value/Tier，ID 不进 prompt）与可选接口 `ContextAwareExtractor`——仿 `RevisionExtractor` 先例，不改既有 `LLMExtractor` 签名。
2. **`pkg/memory/sdk.go`**：`Extract` 前取 `beliefContext`——active + candidate、仅 Tier2/3（Tier1 硬偏好永不进入 prompt，抽取链不可撤回硬偏好）、最近优先截断 10 条、任何存储错误静默降级为原通道。LLM 通道结果仅传给 `ResolveAndSaveAligned`（确定性/反馈通道不带 beliefs， rogue 标签无从生效）。
3. **`pkg/memory/conflict.go`**：`ResolveAndSaveAligned`（旧 `ResolveAndSave` 委托它，签名与语义不变）——`retract` 指向有效目标 → 先 supersede 再常规新建（撤回链指向新记忆）；`reinforce` → 复用 Case 2 强化主体（抽为 `reinforceExisting`），措辞更新不构成冲突，two-strike 晋升照常。`resolveAlignmentTarget` 做护栏：action 取值合法、序号落在展示范围内、目标非 Tier1 且仍为 active/candidate，任何不满足静默降级为无标签路径。
4. **`internal/memory/deepseek_extractor.go`**：`ExtractFromArticle/ExtractFromRevision` 委托到带 beliefs 的内部实现（beliefs 为空时 prompt 与原版一致）；新增 `ExtractFromSessionWithBeliefs`——按会话是否携带改前稿选通道，prompt 附加序号化 belief 清单与三值标注规则（reinforce 必须复用旧 key；拿不准宁可不标）。
5. **测试**：`pkg/memory/conflict_alignment_test.go`（retract 修 key 漂移并补撤回链、reinforce 修措辞降级/候选晋升、越界与 Tier1 双保险、死目标降级、无标签路径保持 Case 3、beliefContext 过滤/排序/截断）；`internal/memory/deepseek_extractor_test.go`（新字段解析、旧形状不变、belief 块渲染含 ID 不进 prompt 断言）。
6. **附带修复（存量缺陷，A/B 驱动实测钓出）**：`Store` 接口新增 `Update`（按主键 UPDATE），`PgStore` 实现之；强化路径统一改走 `Update`。原 `Save` 是以 `(user_id, category, key, status)` 为冲突目标的 upsert——two-strike 候选晋升改变 status 元组后冲突不命中，同 id INSERT 撞 `user_memories_pkey`，**改前 Case 2 的晋升对 PgStore 一直静默失败**（仅 WARN，候选永远升不了 active）。fake/mock store 同步补齐该方法。

## 语义保持

- 提取器未实现 `ContextAwareExtractor`、用户无 Tier2/3 记忆、beliefs 拉取失败——行为与改前逐字节一致（既有 `conflict_evidence_test.go` 全部原样通过）。
- 对齐标签只在"prompt 实际展示过的 belief"集合上生效；撤回目标上限定为 Tier2/3，Tier1 仍只有显式 API 路径可 supersede。
- 差评（GradeNegative）仍跳过 Tier2 保存，坏稿不会撤回偏好。

## 验证（容器 `golang:1.25`，挂仓库根）

- `go build ./...` 全树通过；`go test -p 1 -count=1 ./...` 全绿（DB 门控的 CI 对齐用例本机按惯例 skip，与本改动无关）。
- `pkg/memory` + `internal/memory` 两包含新增 13 个用例全绿。
- **真实抽取链 A/B（商业线 `cmd/pref-evolution-ab`，DeepSeek-V4-Flash @0.7，24 会话）**：同二进制双臂（new=对齐通道 / old=剥除可选接口回退精确匹配）× 3 剧本（同题措辞漂移 ×4 / 真实演化 ×5 / 独立新增 ×3），new 臂 8/8 判据通过——恒定偏好（title/tone）措辞复述零撤回且 active occ=4、真实演化撤旧无矛盾并存、独立新增零误撤回；old 臂每个维度都出现 superseded/candidate 震荡（实测区分度成立）。驱动同时钓出 `Store.Update` 存量缺陷（见变更 6）。

## 已知边界

- belief 清单按最近优先截断 10 条：极多偏好的用户，最旧的可对齐 belief 不进 prompt——降级为原精确匹配路径，不劣于改前。
- `reinforce` 的措辞刷新以模型判断为准（"含义一致仅措辞不同"）；误标 reinforce 只会更新措辞不增删记忆，误标 retract 的爆炸半径为 prompt 内 10 条之一且需过护栏校验。
