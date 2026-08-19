# WritingAgentBench V2 数据层

## 状态

Task 8 建立并行的 WABench Schema v1 数据层。旧 `evaluation_sets`、`evaluation_samples`、`evaluation_runs` 保持不变，继续作为 legacy 只读来源；它们的旧评分不能作为 WABench 发布门禁证据。

## 表与边界

Migration `063_wabench_data_layer` 新增：

- `wabench_suites`：版本、正式分区、可见性、覆盖和隐私策略；
- `wabench_source_fixtures`：来源哈希以及公开正文、私有引用或仅哈希存储；
- `wabench_cases`：任务、难度、来源、Rubric、能力、风险和 Legacy 溯源；
- `wabench_candidates`：Prompt、Memory、模型、代码、工具和 feature flag 冻结信息；
- `wabench_runs / outputs / checks`：执行、失败、路由、延迟和确定性检查；
- `wabench_reviews / outcomes`：五项 1—5 分、评审溯源和不含修改正文的用户行为；
- `wabench_gate_decisions`：发布结论、证据、例外和回滚条件。

正式分区只允许：`development`、`public_holdout`、`private_holdout`、`red_team`、`live_probe`。`migration_candidate` 是导入状态，不是第六种分区。

## 风格 Profile

V2 当前有三种内置风格：`yinyue`、`shenlun`、`xiaohongshu`，同时支持用户自定义风格。WABench 不把风格限制为三项枚举：

- 内置风格引用：`luminbuddy.builtin-style.<slug>`；
- 用户自定义风格引用：`luminbuddy.user-style.<profile_uuid>.v<version>`；
- 无法从旧数据确定用户和版本的自定义 slug：暂存为 `luminbuddy.legacy-style.<slug>`，并产生必须人工处理的迁移警告。

用户自定义引用绑定不可变 `user_style_profile_versions`，避免同名 slug、跨用户和后续修改造成评测漂移。

## Legacy importer

Importer 只读取旧表并幂等 upsert 到新表：

```bash
cd backend
go run ./cmd/wabench-import --apply --partition development
```

安全规则：

1. Legacy 导入只能创建私有套件；
2. 原始 `input_prompt` 不复制到新表，只保存 SHA-256 和 `legacy:evaluation_samples/<id>` 引用；
3. 原 ID 保存在 `legacy_set_id / legacy_sample_id`；
4. 原 `scoring_criteria` 原样保存在 `legacy_score`，只供诊断；
5. 缺失的任务类型、难度、风险标签、风格所有者和版本进入 `migration_warnings`；
6. 重复执行按 Legacy ID 更新，不创建重复记录；
7. CLI 只输出数量和警告类型统计，不输出输入正文。

导入后的套件状态是 `migration_candidate`。只有完成隐私、重复、任务覆盖、风险和评分锚点复核后，才能进入正式运行或发布门禁。
