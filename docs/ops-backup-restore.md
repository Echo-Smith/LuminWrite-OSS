# 备份与恢复 Runbook

自托管部署的数据全部在两个 Docker volume 和一个 env 文件里。本文覆盖
日常备份、迁移到新机、灾难恢复三类场景。命令均假设使用主
`docker-compose.yml`（容器名前缀 `luminbuddy-v2-`）；quickstart 栈把
`luminbuddy-v2-` 换成 `luminwrite-quickstart-` 即可。

## 数据清单

| 内容 | 位置 | 说明 |
|---|---|---|
| 全部业务数据 | volume `pg_data`（PostgreSQL 17） | 文档、会话、风格、记忆、KB、治理台账 |
| 文档解析临时文件 | volume `docreader_tmp` | 可丢弃，无需备份 |
| 密钥与模型配置 | `.env.docker` | 含 LLM key，**与备份分开保管** |

## 日常备份（建议每日）

```bash
# 逻辑备份（推荐，跨版本安全）
docker exec luminbuddy-v2-pg pg_dump -U postgres -d writing_agent_v2 \
  --format=custom --file=/tmp/luminwrite-$(date +%F).dump
docker cp luminbuddy-v2-pg:/tmp/luminwrite-$(date +%F).dump .
docker exec luminbuddy-v2-pg rm /tmp/luminwrite-$(date +%F).dump

# 连同 env 一起归档（chmod 600，含密钥）
tar czf luminwrite-env-$(date +%F).tar.gz .env.docker
```

可用 cron / 1Panel 计划任务跑以上脚本，保留 7–30 天滚动窗口。

## 恢复

```bash
# 1. 停止写入方（保留 PG 容器运行）
docker compose stop backend

# 2. 恢复数据库
docker cp luminwrite-2026-09-12.dump luminbuddy-v2-pg:/tmp/restore.dump
docker exec luminbuddy-v2-pg pg_restore -U postgres -d writing_agent_v2 \
  --clean --if-exists /tmp/restore.dump
docker exec luminbuddy-v2-pg rm /tmp/restore.dump

# 3. 重启
docker compose start backend
```

`--clean` 会先删除同名对象再导入，因此恢复到**已初始化过的库**是幂等的；
恢复到全新库则去掉该参数。恢复后 `curl http://127.0.0.1:8080/health`
并在前端抽查一篇历史文档确认内容完整。

## 迁移到新机

1. 旧机：执行日常备份，得到 `.dump` + `.env.docker`；
2. 新机：克隆仓库（或解压部署包），放置 `.env.docker`（改 `POSTGRES_PASSWORD`
   需同步改备份恢复时的连接串）；
3. 新机启动：`docker compose up -d postgres && docker compose stop backend`
   （等 pg 健康），按上文恢复数据库；
4. `docker compose up -d` 全量启动，验证登录与文档。

迁移**不会**带走的东西：各浏览器本地保存的 Passkey 绑定（WebAuthn 凭据与
域名绑定，换域名后需重新注册）。

## 灾难场景速查

| 症状 | 处置 |
|---|---|
| PG 容器无法启动 | `docker volume inspect luminbuddy-v2_pg_data` 确认 volume 存在；损坏则删除 volume → `docker compose up -d postgres`（迁移自动重建空库）→ 按上文恢复 |
| 只丢部分数据（误删文档） | 用最近的 dump 起临时实例：`docker run -d --name pg-restore -e POSTGRES_PASSWORD=… paradedb/paradedb:v0.22.2-pg17` → 恢复到临时实例 → 导出所需表 `pg_dump -t writing_documents …` → 回灌 |
| `.env.docker` 丢失 | 数据可完整恢复，但 LLM key、WebAuthn RP 配置需重新填写；Admin 后台热配置的模型 key 存于数据库（加密），恢复数据库后可用 |
