# API Manager

API Manager 是一个可私有化部署的 API 管理与网关服务，提供管理控制台、API 路由、鉴权、限流、配额、上游代理、WASM 插件、RBAC、审计日志、Prometheus 指标和 OpenTelemetry 链路追踪。

本仓库仅包含构建 Docker 镜像所需的程序源码，以及 Docker Compose、Helm、监控和生产运维配置。发布镜像位于私有 Docker Hub 仓库：

```text
dingding229/api-manager
```

## 技术栈

| 分类 | 组件 |
| --- | --- |
| 服务端 | Go 1.26、`net/http` |
| 数据库 | PostgreSQL、pgx |
| 分布式限流 | Redis |
| 插件运行时 | WebAssembly、wazero |
| 身份与权限 | 管理员引导令牌、用户 JWT、RBAC |
| API 鉴权 | API Key、JWT HS256、HMAC-SHA256 |
| 日志 | Go `slog` JSON 结构化日志、Grafana Loki、Grafana Alloy |
| 指标与告警 | Prometheus、Alertmanager、Grafana |
| 链路追踪 | OpenTelemetry OTLP、Grafana Tempo |
| 部署 | Docker、Docker Compose、Helm、Kubernetes |

## 获取私有镜像

先使用有权限的 Docker Hub 账号登录：

```bash
docker login
```

拉取最新版：

```bash
docker pull dingding229/api-manager:latest
```

生产环境应使用经过审核的不可变摘要，而不是可变的 `latest` 标签：

```bash
docker pull dingding229/api-manager@sha256:<image-digest>
```

支持的镜像平台：

- `linux/amd64`
- `linux/arm64`

## 单容器运行

以下方式使用内存存储和内存限流，重启后管理数据会丢失，仅适合快速验证镜像和控制台：

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
  -v api-manager-plugins:/data/plugins \
  -v api-manager-plugin-library:/data/plugin-library \
  dingding229/api-manager:latest
```

访问地址：

- 控制台：`http://127.0.0.1:8080/console/`
- 存活检查：`http://127.0.0.1:8080/health/live`
- 就绪检查：`http://127.0.0.1:8080/health/ready`
- Prometheus 指标：`http://127.0.0.1:8080/metrics`

首次进入控制台时，使用 `ADMIN_TOKEN` 完成管理员初始化。初始化完成后使用创建的管理员账号登录。

## Docker Compose 一体化部署

`compose.yaml` 会启动：

- API Manager
- PostgreSQL
- Redis
- Loki
- Alloy
- Tempo
- Prometheus
- Alertmanager
- Grafana

创建本地 `.env`，所有密钥必须分别生成，不得复用：

```bash
cat > .env <<EOF
API_MANAGER_IMAGE=dingding229/api-manager:latest
ADMIN_TOKEN=$(openssl rand -hex 32)
USER_JWT_SECRET=$(openssl rand -hex 32)
CREDENTIAL_ENCRYPTION_KEY=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -hex 32)
GRAFANA_ADMIN_PASSWORD=$(openssl rand -hex 32)
EOF
chmod 600 .env
```

登录 Docker Hub 并启动：

```bash
docker login
docker compose pull
docker compose up -d
```

查看状态和日志：

```bash
docker compose ps
docker compose logs -f api-manager
```

停止服务：

```bash
docker compose down
```

若需要同时删除数据库、Redis、插件和监控数据卷：

```bash
docker compose down -v
```

默认仅在本机监听以下端口：

| 服务 | 地址 |
| --- | --- |
| API Manager | `http://127.0.0.1:8080` |
| Grafana | `http://127.0.0.1:3000` |
| Prometheus | `http://127.0.0.1:9090` |
| Alertmanager | `http://127.0.0.1:9093` |
| Loki | `http://127.0.0.1:3100` |
| Tempo | `http://127.0.0.1:3200` |
| Alloy | `http://127.0.0.1:12345` |

## 生产 Docker Compose 部署

`compose.production.yaml` 只部署 API Manager。生产环境的 HTTPS 反向代理、PostgreSQL、Redis、指标采集和 OTLP 后端由部署方提供。

生产运行时会强制检查：

- 镜像必须通过 `API_MANAGER_IMAGE` 指定，建议固定为 `@sha256:<digest>`。
- PostgreSQL 必须启用 TLS，并在 DSN 中使用 `sslmode=verify-full`。
- Redis 必须启用 TLS 并设置独立密码。
- `ADMIN_TOKEN`、`USER_JWT_SECRET`、`CREDENTIAL_ENCRYPTION_KEY` 和 `METRICS_TOKEN` 必须互不相同且至少 32 个字符。
- 生产模式不允许 PostgreSQL 或 Redis 降级为内存实现。
- 生产模式不允许通配符 CORS。
- 启用 OpenTelemetry 时必须使用带 TLS 的 OTLP 端点。

### 1. 创建文件型 Secret

Secret 目录必须位于仓库之外：

```bash
python3 scripts/init-production-secrets.py --dir /srv/api-manager/secrets
```

脚本会创建：

- `admin_token`
- `user_jwt_secret`
- `credential_encryption_key`
- `metrics_token`

另行写入 PostgreSQL DSN 和 Redis 密码：

```bash
read -rsp 'PostgreSQL DSN: ' POSTGRES_DSN; echo
printf '%s\n' "$POSTGRES_DSN" > /srv/api-manager/secrets/postgres_dsn
unset POSTGRES_DSN

read -rsp 'Redis password: ' REDIS_PASSWORD; echo
printf '%s\n' "$REDIS_PASSWORD" > /srv/api-manager/secrets/redis_password
unset REDIS_PASSWORD

chmod 700 /srv/api-manager/secrets
chmod 444 /srv/api-manager/secrets/*
```

PostgreSQL DSN 示例：

```text
postgres://api_manager:<password>@postgres.internal:5432/api_manager?sslmode=verify-full
```

### 2. 设置部署变量

```bash
export API_MANAGER_IMAGE='dingding229/api-manager@sha256:<image-digest>'
export PROD_SECRETS_DIR='/srv/api-manager/secrets'
export PROD_REDIS_ADDR='redis.internal:6380'
export PROD_REDIS_USERNAME='api-manager'
export API_MANAGER_LOCAL_PORT='8080'
```

可选 OpenTelemetry 配置：

```bash
export PROD_OTEL_ENABLED='true'
export PROD_OTEL_ENDPOINT='otel-collector.internal:4317'
export PROD_OTEL_INSECURE='false'
```

### 3. 预检并启动

```bash
python3 scripts/preflight-production.py
docker compose --env-file /dev/null -f compose.production.yaml pull
docker compose --env-file /dev/null -f compose.production.yaml up -d
```

部署后执行非破坏性检查：

```bash
python3 scripts/verify-production.py \
  --url http://127.0.0.1:8080 \
  --secret-dir "$PROD_SECRETS_DIR"
```

### 4. 私有 CA

当 PostgreSQL 或 Redis 使用私有 CA 时，将以下只含公钥证书的文件放在独立目录：

```text
/srv/api-manager/ca/redis-ca.pem
/srv/api-manager/ca/postgres-ca.pem
```

PostgreSQL DSN 还需加入容器内证书路径：

```text
sslmode=verify-full&sslrootcert=/run/certs/postgres-ca.pem
```

启动时叠加覆盖配置：

```bash
export PROD_CA_CERTS_DIR='/srv/api-manager/ca'
python3 scripts/preflight-production.py
docker compose --env-file /dev/null \
  -f compose.production.yaml \
  -f compose.production.private-ca.yaml \
  up -d
```

### 5. 备份

备份脚本要求先停止 API 写入，并通过 libpq 的 `PGSERVICE`、`PGSERVICEFILE` 和 `PGPASSFILE` 提供数据库凭证：

```bash
docker compose --env-file /dev/null -f compose.production.yaml stop api-manager

python3 scripts/backup-production.py \
  --output-dir /srv/backups/api-manager \
  --tool-image 'alpine@sha256:<reviewed-image-digest>'
```

备份包含 PostgreSQL 自定义格式转储、插件数据卷、插件库数据卷和 SHA-256 清单。备份完成后仍需在隔离环境中定期执行恢复演练。

## Helm 部署

Chart 路径：

```text
deploy/helm/api-manager
```

私有 Docker Hub 镜像需要 Kubernetes Registry Secret：

```bash
kubectl create namespace api-manager --dry-run=client -o yaml | kubectl apply -f -

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
  --set env.REDIS_ADDR='redis.internal:6380' \
  --set env.REDIS_TLS_ENABLED='true' \
  --set env.OTEL_EXPORTER_OTLP_INSECURE='false'
```

生产环境保持 `replicaCount: 1`。当前插件注册表位于进程内，在实现分布式插件热加载之前不应横向扩容。

## 配置

### 核心环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | HTTP 监听地址 |
| `PRODUCTION_MODE` | `false` | 启用生产环境强校验 |
| `ADMIN_TOKEN` | 无 | 首次管理员初始化令牌，至少 32 字符 |
| `USER_JWT_SECRET` | 无 | 控制台用户会话 JWT 密钥，至少 32 字符 |
| `USER_JWT_TTL` | `12h` | 登录会话有效期，范围为 1 秒至 24 小时 |
| `CREDENTIAL_ENCRYPTION_KEY` | 无 | 调用凭证可逆加密密钥，必须稳定保存 |
| `POSTGRES_DSN` | 无 | PostgreSQL 连接 URL；未设置时使用内存存储 |
| `USE_REDIS` | 根据 `REDIS_ADDR` 推断 | 是否启用 Redis 限流 |
| `REDIS_ADDR` | `redis:6379` | Redis `host:port` |
| `REDIS_USERNAME` | 无 | Redis ACL 用户名 |
| `REDIS_PASSWORD` | 无 | Redis 密码 |
| `REDIS_DB` | `0` | Redis 数据库编号 |
| `REDIS_TLS_ENABLED` | `false` | 是否启用 Redis TLS |
| `REDIS_TLS_CA_FILE` | 无 | Redis 私有 CA 文件路径 |
| `METRICS_TOKEN` | 无 | `/metrics` Bearer Token；生产环境必填且至少 32 字符 |
| `LOG_LEVEL` | `info` | 日志级别 |
| `SHUTDOWN_TIMEOUT` | `10s` | 优雅退出超时 |
| `MAX_BODY_BYTES` | `1048576` | 普通请求体大小限制 |
| `CORS_ORIGINS` | 空 | 逗号分隔的允许来源；生产环境禁止 `*` |
| `API_UPSTREAM_CREDENTIALS` | 空 | 服务端托管的上游凭证 JSON |

### 插件配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PLUGIN_DIR` | `plugins` | 已安装插件目录 |
| `PLUGIN_LIBRARY_DIR` | `plugin-library` | 插件库目录 |
| `PLUGIN_MAX_BYTES` | `20971520` | 插件上传大小上限 |
| `PLUGIN_DATABASE_WRITES_ENABLED` | `false` | 插件数据库写入预留开关；当前不提供写入 ABI |

### OpenTelemetry 配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OTEL_ENABLED` | `false` | 是否启用链路追踪 |
| `OTEL_SERVICE_NAME` | `api-manager` | 服务名称 |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | 空 | OTLP gRPC 端点 |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` | 是否使用明文 OTLP；生产环境必须为 `false` |

### 文件型 Secret

以下敏感配置支持对应的 `*_FILE` 变量：

- `ADMIN_TOKEN_FILE`
- `USER_JWT_SECRET_FILE`
- `CREDENTIAL_ENCRYPTION_KEY_FILE`
- `POSTGRES_DSN_FILE`
- `REDIS_PASSWORD_FILE`
- `API_UPSTREAM_CREDENTIALS_FILE`
- `METRICS_TOKEN_FILE`

同一个配置不能同时设置明文变量和 `*_FILE` 变量。Secret 文件必须是普通文本文件，大小不得超过 64 KiB。

## 数据持久化

| 数据 | Docker Compose 卷 |
| --- | --- |
| PostgreSQL | `postgres_data` |
| Redis | `redis_data` |
| 已安装插件 | `plugin_data` |
| 插件库 | `plugin_library` |
| Loki | `loki_data` |
| Prometheus | `prometheus_data` |
| Tempo | `tempo_data` |
| Grafana | `grafana_data` |
| Alertmanager | `alertmanager_data` |

数据库迁移已嵌入服务二进制，连接 PostgreSQL 后会在启动期间自动执行。

## 运行与安全要求

- 在 API Manager 前部署 HTTPS 反向代理，并只向可信网络开放管理控制台。
- 生产环境固定镜像摘要，避免直接使用 `latest`。
- 不要将 `.env`、Secret 文件、数据库密码、Docker Hub Token 或私钥提交到 Git。
- `ADMIN_TOKEN` 仅用于首次初始化，不是日常管理 API 的登录凭证。
- 保持 `CREDENTIAL_ENCRYPTION_KEY` 稳定；丢失或更换后，已有调用密钥无法再次解密查看。
- PostgreSQL、Redis 和 OTLP 生产连接均应启用 TLS 并验证服务端证书。
- `/metrics` 在生产环境需要 `Authorization: Bearer <METRICS_TOKEN>`。
- 对 PostgreSQL、插件数据卷和插件库执行一致性备份，并定期验证恢复流程。
