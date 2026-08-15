# LuminBuddy V2 — 1Panel 部署指南

## 前置条件

1. 服务器已安装 **1Panel** 面板
2. 1Panel 中已安装 **Docker** 和 **Docker Compose**（应用商店 → Docker）
3. 服务器有至少 **2GB 可用内存**

## 部署步骤

### 方式一：镜像包部署（推荐，无需服务器构建）

打包时使用了 `--images` 参数，已导出预构建的 Docker 镜像，服务器无需 build。

1. **上传两个文件到服务器**
   ```bash
   # 源码包 + 镜像包上传到服务器，例如 /opt/
   scp luminbuddy-v2-main-*.tar.gz luminbuddy-v2-images-*.tar.gz root@your-server:/opt/
   ```

2. **解压源码 + 加载镜像**
   ```bash
   cd /opt
   mkdir -p luminbuddy-v2
   cd luminbuddy-v2
   tar xzf /opt/luminbuddy-v2-main-*.tar.gz
   docker load -i /opt/luminbuddy-v2-images-*.tar.gz
   ```

3. **配置环境**
   ```bash
   # .env.docker 已在包中，按需修改
   vi .env.docker
   ```

4. **在 1Panel 中创建 Compose**
   - 打开 1Panel → 容器 → 编排 → 创建编排
   - 名称：`luminbuddy-v2`
   - 路径：选择 `/opt/luminbuddy-v2` 目录
   - 1Panel 会自动识别 `docker-compose.yml`
   - 点击 **启动**
   - **无需 --build**，镜像已预加载

5. **验证启动**
   - 1Panel 容器列表可看到 4 个容器：
     - `luminbuddy-v2-pg` — PostgreSQL 数据库
     - `luminbuddy-v2-docreader` — 文档解析
     - `luminbuddy-v2-backend` — 后端 API + WebSocket
     - `luminbuddy-v2-frontend` — 前端 Nginx

6. **访问**
   - 本地：`http://your-server-ip:3002`
   - 域名（需配置反向代理）：`https://luminbuddy2.ericdocmic.top/`
   - 后端 API：`http://your-server-ip:8080/health`

### 域名 + HTTPS 部署（1Panel 反向代理）

已有域名 `luminbuddy.ericdocmic.top`，通过 1Panel 配置反向代理：

1. **1Panel → 网站 → 创建网站 → 反向代理**
   - 主域名：`luminbuddy.ericdocmic.top`
   - 代理地址：`http://127.0.0.1:3002`

2. **申请 SSL 证书**
   - 网站设置 → HTTPS → Let's Encrypt → 自动申请
   - 开启「强制 HTTPS」

3. **访问**
   - 打开 `https://luminbuddy.ericdocmic.top` → 自动跳转到 `/v2/`
   - WebSocket 会自动走 `wss://luminbuddy.ericdocmic.top/api/v2/ws/agent`

> 无需改代码。前端 API 调用使用绝对路径 `/api/v2/...`，nginx 会代理到后端。
> WebAuthn 配置已在 `.env.docker` 中设为 `luminbuddy.ericdocmic.top`。

### V1 + V2 共存部署（同域名不同路径）

如果 `luminbuddy.ericdocmic.top` 根路径 `/` 已绑定 V1（端口 3000），
V2 需要通过 `/v2/` 子路径访问，在 1Panel 的网站 Nginx 配置中
添加以下 location 块（放在 `location /` **之前**）：

```nginx
# ── V2 API → V2 后端（端口 8080）──────────────────
# /api/v2/ 是 V2 独有前缀，不会与 V1 冲突
location /api/v2/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    # WebSocket 支持
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";

    # 流式响应长超时
    proxy_read_timeout 120s;
    proxy_send_timeout 120s;
    proxy_buffering off;
}

# ── V2 前端 → V2 Nginx（端口 3002）────────────────
location /v2/ {
    proxy_pass http://127.0.0.1:3002;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";

    proxy_read_timeout 120s;
    proxy_send_timeout 120s;
    proxy_buffering off;
}

# ── V1（根路径 → 端口 3000）──────────────────────
location / {
    proxy_pass http://127.0.0.1:3000;
    # ... 保留 V1 原有的 proxy 配置
}
```

**路由效果：**

| 请求路径 | 转发到 | 说明 |
|---|---|---|
| `luminbuddy.ericdocmic.top/` | `127.0.0.1:3000` | V1 首页 |
| `luminbuddy.ericdocmic.top/v2/` | `127.0.0.1:3002` | V2 前端（自动 SPA 路由） |
| `luminbuddy.ericdocmic.top/api/v2/ws/agent` | `127.0.0.1:8080` | V2 WebSocket |
| `luminbuddy.ericdocmic.top/api/v2/sse/topics` | `127.0.0.1:8080` | V2 SSE 热搜推送 |
| `luminbuddy.ericdocmic.top/api/v2/admin/*` | `127.0.0.1:8080` | V2 Admin API |

> **为什么不把 `/api/v2/` 也转发到 3002？**
> 因为 V2 前端的 nginx（3002）本身也是把 `/api/` 代理到后端（8080），
> 多一层转发会增加延迟。直接转发到 8080 更高效。
> WebSocket 和 SSE 也能直连后端，不受前端 nginx 的缓冲影响。

> **1Panel 操作路径：** 网站 → 点击域名 → 配置 → Nginx 配置（或「自定义伪静态规则」），将上面的 location 块粘贴进去，reload nginx 即可。

### 方式二：命令行部署（有镜像包）

```bash
# 1. 解压源码
cd /opt
mkdir -p luminbuddy-v2 && cd luminbuddy-v2
tar xzf /opt/luminbuddy-v2-main-*.tar.gz

# 2. 加载镜像（无需 build！）
docker load -i /opt/luminbuddy-v2-images-*.tar.gz

# 3. 配置环境（.env.docker 已在包中，按需修改）
vi .env.docker

# 4. 启动
docker compose up -d   # 无需 --build！

# 5. 查看日志
docker compose logs -f backend

# 6. 查看状态
docker compose ps
```

### 方式三：仅源码部署（服务器自行 build）

```bash
# 解压
cd /opt
tar xzf luminbuddy-v2-main-*.tar.gz
cd luminbuddy-v2

# 配置
cp .env.docker.example .env.docker
vi .env.docker

# 构建并启动
docker compose up -d --build
```

> ⚠️ 国内服务器构建慢，建议使用方式二（镜像包部署）。

## 环境配置

所有配置在 `.env.docker` 文件中，已预填好 API Key。如需修改：

```bash
vi .env.docker
```

### 关键配置项

| 配置 | 说明 | 默认值 |
|---|---|---|
| `POSTGRES_PASSWORD` | 数据库密码 | `postgres` |
| `BACKEND_PORT` | 后端端口 | `8080` |
| `FRONTEND_PORT` | 前端端口 | `3002` |
| `AGENT_MODE` | Agent 模式 (`unified`/`pipeline`) | `unified` |
| `WEBAUTHN_RP_ID` | WebAuthn 域名 | `localhost` |

### 修改 WebAuthn 域名（如需域名访问）

```bash
# 编辑 .env.docker
WEBAUTHN_RP_ID=your-domain.com
WEBAUTHN_RP_ORIGIN=https://your-domain.com
```

## 常用运维命令

```bash
# 查看日志
docker compose logs -f backend    # 后端日志
docker compose logs -f frontend   # 前端日志
docker compose logs -f postgres   # 数据库日志

# 重启服务
docker compose restart backend

# 停止所有服务
docker compose down

# 重新构建（代码更新后）
docker compose up -d --build

# 查看数据库
docker exec -it luminbuddy-v2-pg psql -U postgres -d writing_agent_v2
```

## 架构说明

```
端口 3002 (Nginx)     端口 8080 (Go)        端口 5432 (PG)
┌──────────┐         ┌──────────┐          ┌──────────┐
│ Frontend │ ──API──▶│ Backend  │ ────────▶│PostgreSQL│
│ (Nginx)  │  /api/  │ (Go WS)  │  DB conn │+pgvector │
└──────────┘         └────┬─────┘          │+paradedb │
                          │gRPC             └──────────┘
                   ┌──────▼─────┐
                   │  Docreader │ (文档解析 sidecar)
                   └────────────┘
```

## 包含的组件

- **Backend (Go)**: 写作 Agent 引擎 + WebSocket + 多源搜索 + 记忆系统
- **Frontend (React)**: 写作界面 + Admin Dashboard + 反馈系统
- **PostgreSQL**: paradedb (BM25) + pgvector (语义检索) + GraphRAG
- **Docreader**: PDF/Word/图片文档解析
- **Skills (知识库)**:
  - `ima-skill`: IMA 知识库检索
  - `jiaozhen-factcheck`: 腾讯较真事实核查
  - `tencent-news`: 腾讯新闻搜索

## 故障排查

### 后端启动失败
```bash
docker compose logs backend | tail -50
```

### 数据库迁移
```bash
docker compose restart backend  # 自动执行迁移
```

### 端口冲突
修改 `.env.docker` 中的 `BACKEND_PORT` 和 `FRONTEND_PORT`

### 内存不足
```bash
# 查看资源占用
docker stats
# 调整 docker-compose.yml 中的 deploy.resources.limits.memory
```
