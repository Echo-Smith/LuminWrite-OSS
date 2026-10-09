# 35 — 双线同步策略：按里程碑 cherry-pick + 适配 commit

> 状态：已生效（2026-10-10，取代 `sync-commercial.sh` 的「glob 文件归属 + 双推」机制）。

## 1. 为什么废弃 glob 共享

旧机制（`scripts/sync-commercial.sh`，已随 2026-08-27 稳定化归档到
`recovery/`）假设：文件要么属于商业（glob 命中 → 推私仓），要么属于开源
（→ 推公开仓），少数 `SHARED_FILES` 双推。三条假设均已失效：

1. **共享文件名存实亡**：`SHARED_FILES`（server.go、personal-center.tsx、
   memory-section.tsx、shared.tsx）在两线已实质分歧——同一文件里 OSS 的
   BYOK/模型服务 与商业的 billing/wallet 是两套互斥的 UI；「双推」等于互相
   覆盖。
2. **同名文件不同语义**：`edition.ts` 两线取值机制不同（OSS 字面量利于
   tree-shaking，商业派生表达式）；`skills-section.tsx` 的组件库 import 路径
   两线不同（`admin-ui` vs `admin`）。
3. **glob 无法表达「同一文件的部分行」**：2026-10 的素材重构中，
   `settings-store.ts` 一份文件里混着两条线的独立改动（OSS 删研究综述开关、
   商业改 lastStyle 默认值），文件级归属判定直接失真。

结论：两线已是**两个产品**（BYOK 个人工具 vs 托管多租户），共享的是「特性
意图」而非「文件内容」。同步的单位应该是**里程碑级 commit**，适配发生在
cherry-pick 之后的适配 commit 里。

## 2. 新机制

```
OSS (feat/* → main)  ──cherry-pick──▶  商业 main
        ▲                                   │
        └──── 反向同步（商业先行特性）───────┘
```

1. **特性在 OSS 先行**（OSS 是公开仓，评审与 CI 在这边）；
2. **里程碑 = 一个可独立验证的 commit**（本仓惯例：conventional commits +
   每 commit 编译/测试自洽）；
3. **移植 = `git fetch oss && git cherry-pick <commit>`**（商业仓已配置
   `oss` remote 指向本地 OSS checkout），冲突解不了一律「以商业语义重写」
   而不是机械合；
4. **cherry-pick 之后必须跟一个适配 commit**，处理三类差异：
   - import 路径（`@/components/admin-ui` → `@/components/admin`）；
   - 商业独有依赖（billing/wallet/pricing 的接线点）；
   - edition 差异（BYOK 等 OSS-only 特性在商业线留桩或明确不做）。
5. **`.commercial-files` 保留但降级**：只用于「哪些文件永远不进开源仓」
   （计费、支付、专有搜索源、迁移 047/062/068-085、商业技能包等资产清单），
   不再作为同步路由。

## 3. 已固化的适配点速查（cherry-pick 后先查这里）

| 差异 | OSS | 商业 | 处理 |
|---|---|---|---|
| 管理组件库 | `@/components/admin-ui` | `@/components/admin` | 改 import |
| KB 图谱面板 | `pages/personal/kb-maintenance/components` | `pages/console/sections/knowledge-base/components` | 改 import |
| 个人中心菜单 | history/models/usage/cron/sensitive/kb-maintenance | wallet/devices | 按商业菜单重排 |
| edition.ts | `EDITION: "oss"` 字面量 | `EDITION: "commercial"` 派生 | 两线各自保留，**禁止互拷** |
| migration 编号 | OSS 先行（至 127） | 商业同号跟进；OSS-only 特性（如 122 BYOK）不跟 | 预先核对 `.commercial-files` 迁移段 |
| labs 条目 | 工作台 + 三个进阶面板 | 工作台 + 深度研究说明卡 + 稿纸模式 | 条目级适配 |
| billing 接线点 | 无 | sidebar/个人中心/console | 不可被 OSS patch 触碰 |

## 4. 流程清单（每次移植照此执行）

1. `cd writing-agent-v2-commercial && git fetch oss <branch>`
2. 按 OSS 里程碑顺序 `git cherry-pick <sha>`；冲突以商业语义重写；
3. 适配 commit：按 §3 速查表过一遍（import 路径 / billing 接线点 / edition）；
4. 验证：`go build ./... && go test -p 1 ./...`（后端）+ `tsc -b && npm run
   lint && npm run build && npm run test`（前端）；
5. 若 OSS patch 触及 `.commercial-files` glob 内文件：停下来人工裁决，不许
   机械合并；
6. commit message 标注来源：`… from OSS (<sha>)`（沿用现有惯例）。

## 5. 本文件与 `.commercial-files` 的关系

`.commercial-files` 继续充当「商业资产清单」（新增商业文件时仍需登记），
但其「同步路由」职责终止。今后若再需要自动同步工具，应以本文件 §4 的清单
为规格重写，而不是复活 `sync-commercial.sh`。
