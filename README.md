# API Manager

API Manager 是可私有化部署的 API 网关与管理平台。提供 API 路由、上游代理、调用鉴权、限流与配额、WASM 插件、用户与 RBAC、审计日志及运行观测控制台。

Docker Hub 镜像：`dingding229/api-manager`（需有拉取权限；支持 `linux/amd64`、`linux/arm64`）。

## 功能与架构

- API 路由发布、版本回滚；API Key、JWT HS256 和 HMAC-SHA256 鉴权。
- 请求体限制、Schema 校验、上游超时/重试/熔断；本地或 Redis 分布式限流。
- WASM 插件管理、控制台用户与角色、审计日志。
- 内置日志、指标、Trace、告警和 Dashboard；`/metrics` 提供 Prometheus 格式指标，可选外部 OTLP 导出。
- 镜像内包含 Loki、Alloy、Tempo、Prometheus、Alertmanager、Grafana 六个程序，可按需在同一容器中启动。该模式是单实例部署，不提供各组件独立的资源隔离或高可用。

服务端使用 Go；持久化管理数据使用 PostgreSQL。未配置 PostgreSQL 时使用内存存储，重建容器会丢失管理数据。

## Docker Compose 快速启动

需要 Docker Compose、Docker Hub 镜像拉取权限。创建仅供本机使用的 `.env`：

```bash
cat > .env <<EOF_ENV
API_MANAGER_IMAGE=dingding229/api-manager:latest
ADMIN_TOKEN=$(openssl rand -hex 32)
USER_JWT_SECRET=$(openssl rand -hex 32)
CREDENTIAL_ENCRYPTION_KEY=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -hex 32)
EOF_ENV
chmod 600 .env
docker login
docker compose pull
docker compose up -d
```

`compose.yaml` 启动 API Manager、PostgreSQL 和 Redis，数据保存在 Docker 卷。控制台位于 `http://127.0.0.1:8080/console/`；首次访问使用 `.env` 中的 `ADMIN_TOKEN` 初始化超级管理员，此后使用账号登录。数据库与 Redis 不向宿主机公开。端口 3000 仅在启用 Grafana 时可用，两个宿主机端口均绑定回环地址；远程访问应通过 HTTPS 反向代理并配置访问控制。

```bash
docker compose ps
docker compose logs -f api-manager
curl -f http://127.0.0.1:8080/health/ready
```

`docker compose down` 停止服务并保留数据卷；`docker compose down -v` **会删除数据卷**。

### 启用完整观测栈

内置运行观测默认启用，无需启动六组件。需要 Loki、Alloy、Tempo、Prometheus、Alertmanager 和 Grafana 时，在 `.env` 中另外设置：

```text
OBSERVABILITY_STACK_ENABLED=true
GRAFANA_ADMIN_PASSWORD=<独立的至少 32 字符密码>
```

然后运行 `docker compose up -d`；Grafana 位于 `http://127.0.0.1:3000/`，用户名 `admin`。其余观测服务只监听容器回环地址。为容器预留至少 2 GiB 内存及充足的持久化磁盘空间。对长期保留、跨实例聚合或高可用有要求时，应使用独立部署的观测服务。

| 配置 | 默认值 | 用途 |
| --- | --- | --- |
| `OBSERVABILITY_STACK_ENABLED` | `false` | 启用镜像内完整观测栈 |
| `GRAFANA_ADMIN_PASSWORD` / `GRAFANA_ADMIN_PASSWORD_FILE` | 空 | 启用完整观测栈时必填，至少 32 字符 |
| `OBSERVABILITY_DIR` | `data/observability` | 日志与 Trace 持久化目录 |
| `OBSERVABILITY_MAX_LOGS` | `5000` | 内存日志条数上限 |
| `OBSERVABILITY_MAX_TRACES` | `2000` | 内存 Span 条数上限 |
| `OBSERVABILITY_FILE_MAX_BYTES` | `16777216` | 单个 JSONL 文件的轮转阈值 |

## 生产部署（外部 PostgreSQL / Redis）

`compose.production.yaml` 只部署 API Manager。提前准备受信任证书签发的 PostgreSQL、启用 TLS 的 Redis，以及 HTTPS 反向代理。容器使用系统信任根验证服务端证书，不提供私有 CA 挂载配置；PostgreSQL DSN 必须使用 `sslmode=verify-full`。生产镜像须使用完整的 `sha256` 摘要固定。

1. 在仓库外新建 Secret 目录并生成四个应用密钥：

   ```bash
   python3 scripts/init-production-secrets.py --dir /absolute/path/api-manager-secrets
   ```

   在该目录另建 `postgres_dsn` 和 `redis_password` 两个纯文本文件。`postgres_dsn` 使用非回环主机名、与证书匹配的域名和 `sslmode=verify-full`；所有 Secret 文件必须为普通文件，权限 `0444`（供非 root 容器读取），目录权限 `0700`。请用安全的密钥管理方式写入密码，不要将真实值提交到仓库。

2. 设置部署环境变量并预检：

   ```bash
   export API_MANAGER_IMAGE='dingding229/api-manager@sha256:<完整镜像摘要>'
   export PROD_SECRETS_DIR='/absolute/path/api-manager-secrets'
   export PROD_REDIS_ADDR='redis.example.com:6380'
   python3 scripts/preflight-production.py
   docker compose --env-file /dev/null -f compose.production.yaml config -q
   ```

3. 启动并验证：

   ```bash
   docker login
   docker compose --env-file /dev/null -f compose.production.yaml up -d
   python3 scripts/verify-production.py --url http://127.0.0.1:8080 --secret-dir "$PROD_SECRETS_DIR"
   ```

生产 Compose 默认仅在 `127.0.0.1` 发布 API Manager 的 8080 端口和 Grafana 的 3000 端口。默认不开启六组件；如启用，需同时提供独立的 Grafana 密码并将 `PROD_API_MEMORY_LIMIT` 提高到至少 `2g`。生产环境请优先使用容器内可读的 `GRAFANA_ADMIN_PASSWORD_FILE`；若挂载新文件，应通过单独管理的 Compose 配置显式加入只读挂载。

### 备份

停止写入后执行离线备份；`PGSERVICEFILE` 和 `PGPASSFILE` 指向仓库外的 libpq 配置文件：

```bash
docker compose --env-file /dev/null -f compose.production.yaml stop api-manager
export PGSERVICE=api-manager
export PGSERVICEFILE='/absolute/path/pg_service.conf'
export PGPASSFILE='/absolute/path/.pgpass'
python3 scripts/backup-production.py \
  --output-dir /absolute/path/backups \
  --tool-image 'alpine@sha256:<完整镜像摘要>'
```

备份包含 PostgreSQL dump、插件/插件库/观测卷的归档及 SHA-256 清单。完成后重新启动服务，定期在隔离环境中演练恢复。

## Kubernetes / Helm

Chart 位于 `deploy/helm/api-manager`。先创建命名空间、镜像拉取 Secret 和运行时 Secret，再安装：

```bash
kubectl create namespace api-manager
kubectl -n api-manager create secret docker-registry dockerhub-regcred \
  --docker-server=https://index.docker.io/v1/ \
  --docker-username="$DOCKERHUB_USER" \
  --docker-password="$DOCKERHUB_TOKEN"
# 在 api-manager 命名空间创建 api-manager-secrets，提供
# ADMIN_TOKEN、USER_JWT_SECRET、CREDENTIAL_ENCRYPTION_KEY、
# POSTGRES_DSN、REDIS_PASSWORD 和 METRICS_TOKEN 六个键。
helm upgrade --install api-manager deploy/helm/api-manager \
  --namespace api-manager \
  --set productionMode=true \
  --set image.digest="sha256:<完整镜像摘要>" \
  --set 'imagePullSecrets[0].name=dockerhub-regcred' \
  --set existingSecret=api-manager-secrets \
  --set env.REDIS_ADDR='redis.example.com:6380' \
  --set env.REDIS_TLS_ENABLED=true
```

生产配置要求 PostgreSQL DSN 启用 `verify-full`。Chart 默认 `replicaCount: 1`；进程内插件注册表及短时指标序列不支持直接横向扩容。根据需要配置持久化存储、Ingress、NetworkPolicy 和 ServiceMonitor。

## 常用配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | HTTP 监听地址 |
| `PRODUCTION_MODE` | `false` | 生产安全校验 |
| `ADMIN_TOKEN` | 无 | 首次管理员初始化令牌，至少 32 字符 |
| `USER_JWT_SECRET` | 无 | 控制台 JWT 密钥 |
| `USER_JWT_TTL` | `12h` | 用户会话有效期 |
| `CREDENTIAL_ENCRYPTION_KEY` | 无 | 上游调用凭证加密密钥，必须长期保存 |
| `POSTGRES_DSN` | 无 | PostgreSQL URL；为空时管理数据仅在内存中 |
| `USE_REDIS` | 根据 `REDIS_ADDR` 推断 | Redis 分布式限流开关 |
| `REDIS_ADDR` | `redis:6379` | Redis 主机和端口 |
| `REDIS_USERNAME` / `REDIS_PASSWORD` | 空 | Redis ACL 账号和密码 |
| `REDIS_TLS_ENABLED` | `false` | Redis TLS（使用系统信任根） |
| `METRICS_TOKEN` | 无 | 生产 `/metrics` Bearer Token |
| `CORS_ORIGINS` | 空 | 允许的浏览器来源，生产禁止 `*` |
| `OTEL_ENABLED` | `false` | 额外向外部接收端导出 Trace |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | 空 | OTLP gRPC `host:port` |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` | 明文 OTLP；生产仅允许镜像内回环链路 |
| `PLUGIN_DIR` / `PLUGIN_LIBRARY_DIR` | `plugins` / `plugin-library` | 插件与插件库持久化目录 |
| `PLUGIN_DATABASE_WRITES_ENABLED` | `false` | 预留开关，不代表插件可写数据库 |

`ADMIN_TOKEN`、`USER_JWT_SECRET`、`CREDENTIAL_ENCRYPTION_KEY`、`POSTGRES_DSN`、`REDIS_PASSWORD`、`METRICS_TOKEN`、`GRAFANA_ADMIN_PASSWORD` 和 `API_UPSTREAM_CREDENTIALS` 支持对应的 `*_FILE` 配置。同一项不能同时设置明文值和文件路径。

## 运行与安全

- `/health/live` 用于存活检查，`/health/ready` 用于就绪检查；生产 `/metrics` 需 `Authorization: Bearer <METRICS_TOKEN>`。
- 不要提交 `.env`、Secret、数据库密码和私钥。首次引导令牌不是日常管理凭证。
- 生产必须固定审核过的镜像摘要。外部数据库、Redis 和 OTLP 链路应启用 TLS 并校验服务端证书。
- 备份 PostgreSQL、插件、插件库和观测卷；加密密钥丢失或更换后，已有调用凭证无法再次解密。
