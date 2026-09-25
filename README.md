# API Manager

API Manager 是一个可私有化部署的 API 网关与管理平台，提供管理控制台、API 路由、调用鉴权、限流与配额、上游代理、WASM 插件、用户与 RBAC、审计日志，以及直接编译进程序的运行观测能力。

私有 Docker Hub 镜像：

```text
dingding229/api-manager
```

## 主要能力

- API 路由的创建、编辑、发布、下线、版本记录与回滚
- API Key、JWT HS256、HMAC-SHA256 调用鉴权
- 本地或 Redis 分布式限流、调用配额、请求体限制
- 上游超时、重试、熔断、请求/响应 Schema 校验
- WASM 插件上传、启停、卸载和插件库
- 控制台用户、角色、中文权限说明与完整 RBAC
- 审计日志落库和查询
- 内置日志、指标、链路追踪、告警和运行观测 Dashboard
- 可选的 Prometheus 指标抓取和外部 OpenTelemetry OTLP 导出

## 技术栈

| 分类 | 技术 |
| --- | --- |
| 服务端 | Go 1.26、`net/http` |
| 数据库 | PostgreSQL、pgx；未配置时可使用内存存储 |
| 分布式限流 | Redis |
| 插件运行时 | WebAssembly、wazero |
| 身份与权限 | 管理员引导令牌、用户 JWT、RBAC |
| API 鉴权 | API Key、JWT HS256、HMAC-SHA256 |
| 日志 | Go `slog` JSON 日志、内置检索与 JSONL 轮转持久化 |
| 指标 | 内置快照和 60 分钟序列、兼容 Prometheus 的 `/metrics` |
| 链路追踪 | OpenTelemetry、内置 Span 存储、可选 OTLP gRPC 导出 |
| 告警与可视化 | 内置告警计算、确认状态和 Web Dashboard |
| 部署 | Docker、Docker Compose、Helm、Kubernetes |

## 获取私有镜像

使用有权限的 Docker Hub 账号登录：

```bash
docker login
docker pull dingding229/api-manager:latest
```

生产环境应固定经过审核的镜像摘要：

```bash
docker pull dingding229/api-manager@sha256:<image-digest>
```

镜像支持：

- `linux/amd64`
- `linux/arm64`

## 单容器快速运行

以下示例使用内存管理数据和内存限流。插件和运行观测数据会写入 Docker 卷；接口、用户、角色、凭证和审计数据会在容器重建后丢失，因此仅适合验证镜像。

```bash
export ADMIN_TOKEN="$(openssl rand -hex 32)"
export USER_JWT_SECRET="$(openssl rand -hex 32)"
export CREDENTIAL_ENCRYPTION_KEY="$(openssl rand -hex 32)"

docker run -d \
  --name api-manager \
  --restart unless-stopped \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=32m \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -p 127.0.0.1:8080:8080 \
  -e HTTP_ADDR=:8080 \
  -e ADMIN_TOKEN="$ADMIN_TOKEN" \
  -e USER_JWT_SECRET="$USER_JWT_SECRET" \
  -e CREDENTIAL_ENCRYPTION_KEY="$CREDENTIAL_ENCRYPTION_KEY" \
  -e USE_REDIS=false \
  -e PLUGIN_DIR=/data/plugins \
  -e PLUGIN_LIBRARY_DIR=/data/plugin-library \
  -e OBSERVABILITY_DIR=/data/observability \
  -v api-manager-plugins:/data/plugins \
  -v api-manager-plugin-library:/data/plugin-library \
  -v api-manager-observability:/data/observability \
  dingding229/api-manager:latest
```

访问地址：

- 控制台：`http://127.0.0.1:8080/console/`
- 存活检查：`http://127.0.0.1:8080/health/live`
- 就绪检查：`http://127.0.0.1:8080/health/ready`
- Prometheus 格式指标：`http://127.0.0.1:8080/metrics`

首次进入控制台时使用 `ADMIN_TOKEN` 初始化超级管理员。初始化完成后，引导入口会隐藏，日常管理使用管理员账号登录。

## Docker Compose 部署

`compose.yaml` 只启动三个服务：

- API Manager
- PostgreSQL
- Redis

API Manager 默认提供轻量观测。**同一镜像还打包了官方 Loki、Alloy、Tempo、Prometheus、Alertmanager 和 Grafana 程序**；开启 `OBSERVABILITY_STACK_ENABLED=true` 时它们由 API Manager 在同一容器内管理，无需额外启动这六个容器。

创建 `.env`：

```bash
cat > .env <<EOF
API_MANAGER_IMAGE=dingding229/api-manager:latest
ADMIN_TOKEN=$(openssl rand -hex 32)
USER_JWT_SECRET=$(openssl rand -hex 32)
CREDENTIAL_ENCRYPTION_KEY=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -hex 32)
EOF
chmod 600 .env
```

启动：

```bash
docker login
docker compose pull
docker compose up -d
docker compose ps
```

查看 API Manager 容器日志：

```bash
docker compose logs -f api-manager
```

停止服务：

```bash
docker compose down
```

删除服务及全部数据卷：

```bash
docker compose down -v
```

默认仅向宿主机回环地址发布一个端口：

```text
127.0.0.1:8080 -> api-manager:8080
```

PostgreSQL 和 Redis 仅在 Compose 内部网络监听。对外 HTTPS、域名和访问控制由部署方的反向代理负责。

## 内置运行观测

登录控制台后，进入 **运行观测** 页面可以查看：

- 网关累计请求、错误率、并发请求、平均延迟和 P95 延迟
- 最近 60 分钟请求量与延迟序列
- 认证失败、Schema 校验失败、限流、上游失败、插件失败、重试与熔断指标
- 结构化日志搜索、日志级别过滤、Request ID 和 Trace ID
- OpenTelemetry Span 查询、状态过滤和耗时
- 活动/已恢复告警以及告警确认状态
- 内置观测存储数量、容量上限和持久化状态

内置告警当前按最近 5 分钟数据计算：

- 至少 10 个请求且错误率达到 5%
- 至少 10 个请求且平均延迟达到 1000 ms
- 发生限流拒绝
- 发生上游调用失败
- 发生插件执行失败

日志和 Trace 保存为轮转 JSONL 文件，每类保留当前文件和两级历史文件；内存检索结果同时受记录数上限控制。敏感字段名中包含 `password`、`secret`、`token`、`api_key` 或 `authorization` 的值会在写盘前脱敏。

默认轻量模块面向单实例和中小规模私有部署，不等同于完整六组件。启用上述打包栈后可使用官方程序的查询、通知路由与可视化功能，但本部署仍为单机模式。大规模部署仍可抓取 `/metrics`，并通过 OTLP 将 Trace 额外导出到外部观测平台。

### 内置观测配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OBSERVABILITY_DIR` | `data/observability` | 日志和 Trace JSONL 文件目录；空值表示只使用内存 |
| `OBSERVABILITY_MAX_LOGS` | `5000` | 内存中最多保留的日志条数 |
| `OBSERVABILITY_MAX_TRACES` | `2000` | 内存中最多保留的 Span 条数 |
| `OBSERVABILITY_FILE_MAX_BYTES` | `16777216` | 每个 JSONL 文件轮转前的最大字节数 |
| `OBSERVABILITY_STACK_ENABLED` | `false` | 启动镜像中的官方六组件（需 32 字符以上独立 Grafana 密码） |
| `GRAFANA_ADMIN_PASSWORD[_FILE]` | 空 | Grafana 管理员密码；生产使用挂载的文件 |

## 完整六组件观测栈（单镜像可选）

镜像从六个官方固定版本镜像复制真实可执行程序和 Grafana Web 资源；配置模板用 `go:embed` 编译进 API Manager，启用时写入容器临时目录，数据写入持久卷，并启动子进程。日志由 Alloy 读取应用 JSONL 文件送往 Loki，Tempo 接收 OTLP Span，Prometheus 抓取 `/metrics` 并向 Alertmanager 发送规则告警，Grafana 预置三个数据源与仪表板。**这不是把六个服务重写成一个 Go 二进制**；它们仍然是独立进程，只是随同一 Docker 镜像发布和运行。

```bash
docker build -t api-manager:bundled .
export GRAFANA_ADMIN_PASSWORD="$(openssl rand -hex 32)"
API_MANAGER_IMAGE=api-manager:bundled API_MANAGER_PULL_POLICY=never \
  OBSERVABILITY_STACK_ENABLED=true docker compose up -d
# Grafana: http://127.0.0.1:3000 （admin / 上述密码）
```

Grafana 只映射到宿主机回环地址；其余五个后端只在容器回环接口监听。生产环境需为该容器预留至少 2 GiB 内存（建议根据采集量进一步扩容）、足够磁盘，并对 Grafana 使用独立的长密码。生产 Compose 可通过 `PROD_OBSERVABILITY_STACK_ENABLED=true`、`PROD_GRAFANA_ADMIN_PASSWORD_FILE=/run/secrets/grafana_admin_password` 和 `PROD_API_MEMORY_LIMIT=2g` 启用（密码文件需安全地挂载到容器）；不启用时仍保持默认的轻量内置观测。单容器方案不具备六个独立部署实例的资源隔离与高可用能力，不应代替有这些要求的生产观测平台。

## 生产 Docker Compose

`compose.production.yaml` 只运行 API Manager，使用外部 PostgreSQL、外部 Redis、文件型 Secret 和持久化 Docker 卷。反向代理由部署方单独配置。

### 1. 创建文件型 Secret

选择仓库之外的新目录：

```bash
python3 scripts/init-production-secrets.py --dir /absolute/private/path/api-manager-secrets
```

脚本生成：

- `admin_token`
- `user_jwt_secret`
- `credential_encryption_key`
- `metrics_token`

另外创建：

```text
postgres_dsn
redis_password
```

`postgres_dsn` 必须使用远端地址和 `sslmode=verify-full`，例如：

```text
postgres://api_manager:<password>@postgres.internal:5432/api_manager?sslmode=verify-full
```

### 2. 设置部署变量

```bash
export API_MANAGER_IMAGE='dingding229/api-manager@sha256:<reviewed-digest>'
export PROD_SECRETS_DIR='/absolute/private/path/api-manager-secrets'
export PROD_REDIS_ADDR='redis.internal:6380'
```

可选设置：

```bash
export API_MANAGER_LOCAL_PORT=8080
export PROD_OBSERVABILITY_MAX_LOGS=10000
export PROD_OBSERVABILITY_MAX_TRACES=5000
export PROD_OBSERVABILITY_FILE_MAX_BYTES=67108864
```

### 3. 预检与启动

```bash
python3 scripts/preflight-production.py
docker compose --env-file /dev/null -f compose.production.yaml config -q
docker compose --env-file /dev/null -f compose.production.yaml up -d
```

### 4. 私有 CA

如 PostgreSQL 或 Redis 使用私有 CA，将 `redis-ca.pem` 和 `postgres-ca.pem` 放入仓库外的只读目录，然后叠加：

```bash
export PROD_CA_CERTS_DIR='/absolute/private/path/api-manager-ca'
docker compose --env-file /dev/null \
  -f compose.production.yaml \
  -f compose.production.private-ca.yaml \
  up -d
```

`compose.production.private-ca.yaml` 仅用于挂载内部 CA 证书，不包含私钥，也不会额外发布端口。

### 5. 备份

先停止 API Manager 的写入，再执行离线一致性备份：

```bash
docker compose --env-file /dev/null -f compose.production.yaml stop api-manager

export PGSERVICE=api-manager
export PGSERVICEFILE='/absolute/private/path/pg_service.conf'
export PGPASSFILE='/absolute/private/path/.pgpass'

python3 scripts/backup-production.py \
  --output-dir /absolute/private/path/backups \
  --tool-image 'alpine@sha256:<reviewed-digest>'
```

备份包含：

- `database.dump`
- `plugins.tar`
- `plugin-library.tar`
- `observability.tar`
- SHA-256 清单

必须定期在隔离环境中验证恢复流程。

## Helm 部署

Chart 位于：

```text
deploy/helm/api-manager
```

私有镜像拉取凭证：

```bash
kubectl -n api-manager create secret docker-registry dockerhub-regcred \
  --docker-server=https://index.docker.io/v1/ \
  --docker-username='<dockerhub-user>' \
  --docker-password='<dockerhub-access-token>'
```

创建运行时 Secret：

```bash
kubectl -n api-manager create secret generic api-manager-secrets \
  --from-literal=ADMIN_TOKEN="$(openssl rand -hex 32)" \
  --from-literal=USER_JWT_SECRET="$(openssl rand -hex 32)" \
  --from-literal=CREDENTIAL_ENCRYPTION_KEY="$(openssl rand -hex 32)" \
  --from-literal=METRICS_TOKEN="$(openssl rand -hex 32)" \
  --from-literal=REDIS_PASSWORD='<redis-password>' \
  --from-literal=POSTGRES_DSN='postgres://api_manager:<password>@postgres.internal:5432/api_manager?sslmode=verify-full'
```

生产安装示例：

```bash
helm upgrade --install api-manager deploy/helm/api-manager \
  --namespace api-manager \
  --create-namespace \
  --set productionMode=true \
  --set image.repository=dingding229/api-manager \
  --set image.digest='sha256:<image-digest>' \
  --set 'imagePullSecrets[0].name=dockerhub-regcred' \
  --set existingSecret=api-manager-secrets \
  --set env.REDIS_ADDR='redis.internal:6380'
```

当前插件注册表和 60 分钟指标序列位于进程内，生产环境默认保持 `replicaCount: 1`。在实现分布式插件同步和观测聚合之前不应直接横向扩容。

## 配置

### 核心环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | HTTP 监听地址 |
| `PRODUCTION_MODE` | `false` | 启用生产环境强校验 |
| `ADMIN_TOKEN` | 无 | 首次管理员初始化令牌，至少 32 字符 |
| `USER_JWT_SECRET` | 无 | 控制台用户会话 JWT 密钥，至少 32 字符 |
| `USER_JWT_TTL` | `12h` | 登录会话有效期，范围 1 秒至 24 小时 |
| `CREDENTIAL_ENCRYPTION_KEY` | 无 | 调用凭证可逆加密密钥，必须稳定保存 |
| `POSTGRES_DSN` | 无 | PostgreSQL URL；未设置时使用内存存储 |
| `USE_REDIS` | 根据 `REDIS_ADDR` 推断 | 是否启用 Redis 限流 |
| `REDIS_ADDR` | `redis:6379` | Redis `host:port` |
| `REDIS_USERNAME` | 无 | Redis ACL 用户名 |
| `REDIS_PASSWORD` | 无 | Redis 密码 |
| `REDIS_DB` | `0` | Redis 数据库编号 |
| `REDIS_TLS_ENABLED` | `false` | 是否启用 Redis TLS |
| `REDIS_TLS_CA_FILE` | 无 | Redis 私有 CA 文件路径 |
| `METRICS_TOKEN` | 无 | `/metrics` Bearer Token；生产模式必填且至少 32 字符 |
| `LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error` |
| `SHUTDOWN_TIMEOUT` | `10s` | 优雅退出超时 |
| `MAX_BODY_BYTES` | `1048576` | 普通请求体大小限制 |
| `CORS_ORIGINS` | 空 | 逗号分隔的允许来源；生产模式禁止 `*` |
| `API_UPSTREAM_CREDENTIALS` | 空 | 服务端托管的上游凭证 JSON |

### 插件配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PLUGIN_DIR` | `plugins` | 已安装插件目录 |
| `PLUGIN_LIBRARY_DIR` | `plugin-library` | 插件库目录 |
| `PLUGIN_MAX_BYTES` | `20971520` | 插件上传大小上限 |
| `PLUGIN_DATABASE_WRITES_ENABLED` | `false` | 插件数据库写入预留开关；当前未开放写入 ABI |

### 外部 OpenTelemetry 导出

内置 Trace 始终启用。以下变量仅控制是否将同一批 Span 额外导出到外部 OTLP gRPC 接收端：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OTEL_ENABLED` | `false` | 启用额外 OTLP 导出 |
| `OTEL_SERVICE_NAME` | `api-manager` | OpenTelemetry 服务名称 |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | 空 | OTLP gRPC `host:port` |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` | 是否允许明文 OTLP；生产模式仅允许打包栈的 127.0.0.1:4317 使用明文 |

### 文件型 Secret

以下敏感配置支持对应的 `*_FILE` 变量：

- `ADMIN_TOKEN_FILE`
- `USER_JWT_SECRET_FILE`
- `CREDENTIAL_ENCRYPTION_KEY_FILE`
- `POSTGRES_DSN_FILE`
- `REDIS_PASSWORD_FILE`
- `API_UPSTREAM_CREDENTIALS_FILE`
- `METRICS_TOKEN_FILE`

同一个配置不能同时设置明文变量和 `*_FILE`。Secret 文件必须是普通文本文件，最大 64 KiB。

## 数据持久化

| 数据 | Docker Compose 卷 |
| --- | --- |
| PostgreSQL | `postgres_data` |
| Redis | `redis_data` |
| 已安装插件 | `plugin_data` |
| 插件库 | `plugin_library` |
| 内置日志与 Trace | `observability_data` |

数据库迁移嵌入服务二进制，连接 PostgreSQL 后会在启动期间自动执行。内置指标的累计值和 60 分钟时间序列保存在进程内，服务重启后重新计数；日志和 Trace 会从观测数据卷中的 JSONL 文件恢复最近记录。

## 运行与安全要求

- 在 API Manager 前部署 HTTPS 反向代理，并仅向可信网络开放管理控制台。
- 生产环境固定镜像摘要，不直接使用 `latest`。
- 不要提交 `.env`、Secret 文件、数据库密码、Docker Hub Token 或私钥。
- `ADMIN_TOKEN` 仅用于首次初始化，不是日常管理 API 的登录凭证。
- 保持 `CREDENTIAL_ENCRYPTION_KEY` 稳定；更换后已有调用密钥无法再次解密查看。
- PostgreSQL、Redis 和外部 OTLP 生产连接应启用 TLS 并验证服务端证书。
- `/metrics` 在生产模式下要求 `Authorization: Bearer <METRICS_TOKEN>`。
- 对 PostgreSQL、插件、插件库和内置观测卷执行一致性备份，并定期做恢复演练。
- 内置观测适合单实例私有部署；需要长期保留、高级查询、跨实例聚合或多渠道通知时，应接入外部观测平台。
