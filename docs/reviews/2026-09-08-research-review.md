# feat/research-review 审查记录

日期：2026-09-08。审查实现基线：`7b6d841`；对照基线：`26c279d`（其中 c5c7065 为之前模型测试/限速 WIP 的独立基线提交）。审查聚焦真实入口、运行预算、证据传输、下载边界、合同与完成标准，并执行下述回归；不是对约三万行新增内容的逐行无遗漏保证。

**结论：可推送开发分支供继续开发与审查，不建议合并主分支或上线。** 后端有完整的模拟全链路实现，但真实用户入口和若干边界尚未完成；自动化通过不能替代这几项。

## 尚未解决的发现（按优先级）

### F1 / P1：真实研究运行没有前端创建入口

位置：[research-api.ts](../../frontend/src/lib/research-api.ts:766)。`startResearchRun` 在 mock=false 时直接抛 RESEARCH_UNAVAILABLE，从未发送 document → contract → confirm → plan → run 的创建请求。mock=true 时返回固定 demo ID。故开启真实 worker/后端功能并不能让用户从界面创建研究任务。已有读取与 gate API 联调不覆盖启动流程。

修复要求：将完整研究问题/读者/交付字段/ResearchSpec/材料引用传给真实合同创建流程；使用服务端返回的 ref/hash 继续确认、编译和创建；添加浏览器或 HTTP-mocked 前端启动测试，断言实际请求顺序与最终 run ID。未完成前保留默认关闭。

### F2 / P1：预算守卫既漏计在途阅读，又允许普通 resume 跳过预算

位置：[research_budget.go](../../backend/internal/server/research_budget.go:93)、[同文件恢复豁免](../../backend/internal/server/research_budget.go:104)。每篇之间只累加 attempt.ActualDurationMS，但当前长 read attempt 尚未完成，不能计入当前已读论文耗时；读账错误还 fail-open。首次触发后缓存/事件让守卫永久返回未触界；普通恢复没有新预算审批却继续发起调用。

当前 [design.md](../../specs/research-review/design.md:66) 规定预算触界后交部分包或不足暂停，扩预算走新合同/新运行。fire-once+resume 与此不同。即使外层节点超时/最终结果预算检查还存在，也不能阻止触界后的额外付费子调用。

修复要求：计入当前 attempt 主动耗时与持久子任务用量；预算读取失败停止新调用；触界后足量生成明确的部分包进入 evidence gate，不足则暂停；无新预算时恢复不得继续阅读。用正常时钟/账本测试代替仅 SetForcedSpentMS，加入超过上限后调用计数不增长的断言。

### F3 / P1：DNS 检查与连接分离，可被 DNS rebinding 绕过

位置：[downloader.py](../../services/scholar-worker/src/lumin_scholar/downloader.py:292)。`_validate_url_target` 先解析并检查 IP，随后 `client.stream("GET", url)` 仍按原 hostname 连接，底层传输会再次解析 DNS。若两次结果不同，实际连接可能落到未校验的内网地址。逐次重定向校验无法消除此窗口。本次为源码确认，未访问实际内网地址或构造真实攻击。

修复要求：连接层绑定已验证 IP，正确保留原 hostname 的 Host/TLS SNI 与证书校验，或采用执行相同约束的受控出口代理；不能通过禁用 TLS 校验解决。加入解析结果先公网后私网的 transport 级测试，断言不会连接私网；同时禁用未受控环境代理路径。当前 mock downloader 测试没有验证真正 socket 连接目标。

### F4 / P2：允许 25 MiB 文件，但解析请求上限只有 8 MiB

位置：[api.py](../../services/scholar-worker/src/lumin_scholar/api.py:44)、[research_worker_client.go](../../backend/internal/writingruntime/research_worker_client.go:116)。Go 将全文 base64 内联到 parse 请求，8 MiB 请求体最多容纳不足 6 MiB 原始文件；如 7 MiB 合法 PDF，下载成功后 parse 请求必定413。由此可能被降级到摘要或失去可引用来源，与25 MiB产品限制不符。

修复要求：实现已设计的受控 blob 传输，或统一原始文件/编码后请求的上限并控制内存。加入大于6 MiB、小于25 MiB的端到端下载→解析样本，不能只各测独立下载器和小TXT。

### F5 / P2：禁止联网时只能拒绝，未实现用户材料研究路径

位置：[research_discover.go](../../backend/internal/writingruntime/research_discover.go:82)、[research_policy_test.go](../../backend/internal/writingplan/research_policy_test.go:11)。模板需要 external.research，compiler 拒绝禁联网合同；executor 也直接返回不可用，没有从授权材料建立候选并阅读的分支。零外部调用满足“不能联网”，但不满足 R02“只读取用户授权材料”以及首版用户论文路径。

修复要求：为禁联网合同编译材料发现能力/受控分支，消费 owner material manifest；联网路径也应合并用户论文而非忽略材料。新增“仅上传论文、不联网、仍能走完证据确认与写作”用例；现有 compile rejection 不能计作完整 A02 功能验收。

## 本轮已修复

- **默认构建启用 mock/功能**：改为只有 VITE_RESEARCH_MOCK=on 才启用 mock，只有 VITE_RESEARCH_REVIEW_ENABLED=true 才显示启用入口；无配置不会伪造研究运行。新增 research-defaults.test.ts 回归测试。真实运行创建缺口仍按 F1 保留。
- **普通 Compose 被 Scholar token 阻断**：原 `${SCHOLAR_WORKER_TOKEN:?…}` 在 profile 未启用时也参与插值，直接让普通 `docker compose config/up` 失败。改为可为空的插值，由 worker 的 __main__.py 在实际启动且无 token 时退出2。修复前后用相同无 token、空 env-file 的 config --services 命令验证，已由失败变为正常列出旧五个服务；未降低实际 worker 的认证要求。

## 本轮验证

- 后端：使用现有专用测试 Postgres `luminbuddy-oss-a6-pg` 的临时隔离 schema，`go test ./internal/... -count=1` 完成，22个有测试包全部通过（包括 server 43.192s、writingruntime 27.983s、writingstore 5.339s）。模型门控测试仍可能跳过，不视作 live 通过。
- 前端：修复后 `npm test` **73 passed**；`npm run build` 通过。存在既有大 chunk/import 混用提示，构建未失败。
- Python Worker：`.venv/bin/python -m pytest -W error -q` 通过，**163 passed / 2 skipped**（live 门控项）。本轮未运行真实模型或真实论文源 smoke。
- Compose：无 Scholar token，`docker compose --env-file /dev/null config --no-env-resolution --services` 通过，仅列出默认服务；未启动/部署服务。
- `git diff --check` 通过。未同步或改动商业版的既有未提交文件。

## 完成状态与下一步

先处理 F1–F3，补 F4/F5 的跨组件测试，再执行真实模型 HTTP 全流程、三学科六次人工核查、商业版同步回归和容器构建/灰度演练。T10（AR-012候选）仍不在首版完成前提中。

本次 push 仅发布开发分支及该审查记录，不代表 review approval、合并或生产发布。
