# API Manager

API Manager 是面向私有化部署的 API 网关与管理平台，提供路由发布、上游代理、API 鉴权、限流与配额、WASM 插件、用户与 RBAC、审计日志以及运行观测控制台。

默认镜像来自 Docker Hub：

```text
docker.io/dingding229/api-manager:latest
```

镜像支持 `linux/amd64` 和 `linux/arm64`，包含以下观测组件：

- Loki
- Alloy
- Tempo
- Prometheus
- Alertmanager
- Grafana

镜像内观测栈适用于单实例部署，不提供组件级隔离或高可用。需要长期保留、跨实例聚合或高可用时，应使用独立的外部观测平台。

## 功能

- API Key、JWT HS256、HMAC-SHA256 鉴权。
- 请求大小限制、请求/响应 Schema 校验、上游超时、重试与熔断。
- 本地或 Redis 分布式限流；PostgreSQL 持久化管理数据。
- WASM 插件上传、校验、存储、启用和版本库管理。
- 内置日志、指标、Trace、告警与 Dashboard；`/metrics` 输出 Prometheus 格式指标。
- 上游凭证加密保存，客户端管理凭证不会转发到上游。

## 快速启动

需要 Docker Compose 和 Docker Hub 拉取权限。创建仅供本机使用的 `.env`：

```bash
cat > .env <<EOF_ENV
API_MANAGER_IMAGE=docker.io/dingding229/api-manager:latest
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

控制台地址为 `http://127.0.0.1:8080/console/`。首次访问使用 `ADMIN_TOKEN` 初始化超级管理员，控制台用户密码必须为 **12–72 字节**。数据库和 Redis 不向宿主机发布端口。

```bash
docker compose ps
docker compose logs -f api-manager
curl -f http://127.0.0.1:8080/health/ready
```

`docker compose down` 保留数据卷；`docker compose down -v` 会删除数据卷。

### 启用镜像内观测栈

在 `.env` 中增加：

```text
OBSERVABILITY_STACK_ENABLED=true
GRAFANA_ADMIN_PASSWORD=<独立的至少 32 字符随机密码>
```

然后执行：

```bash
docker compose pull api-manager
docker compose up -d api-manager
```

Grafana 地址为 `http://127.0.0.1:3000/`，用户名为 `admin`。完整观测栈建议为容器预留至少 2 GiB 内存和足够的持久化磁盘空间。

## 更新 Docker 镜像

Compose 使用 Docker Hub 的 `latest` 标签，并配置为每次启动时检查远端镜像。更新时执行：

```bash
docker login
docker compose pull api-manager
docker compose up -d --force-recreate api-manager
```

也可以覆盖镜像地址，但生产预检只接受 Docker Hub 上 `docker.io/dingding229/api-manager:<tag>` 形式的镜像标签：

```bash
export API_MANAGER_IMAGE=docker.io/dingding229/api-manager:latest
```

`latest` 是可变标签。若需要审计或回滚，请在部署系统中记录实际拉取到的镜像摘要。

## 生产部署：Docker Compose

`compose.production.yaml` 只部署 API Manager。生产环境还需要：

- 由受信任证书签发的外部 PostgreSQL，DSN 使用 `sslmode=verify-full`。
- 启用 TLS 和密码认证的外部 Redis。
- HTTPS 反向代理或入口网关。
- Docker Hub 拉取权限。

本项目不提供私有 CA 注入或跳过证书验证的配置。PostgreSQL、Redis、OTLP 和 HTTPS 上游均使用容器系统信任根验证服务端证书。生产模式下，API 上游地址必须使用 HTTPS。

### 1. 创建 Secret 目录

在仓库外创建 Secret 目录，并生成五个相互独立的随机应用 Secret：

```bash
python3 scripts/init-production-secrets.py \
  --dir /absolute/path/api-manager-secrets
```

脚本生成：

- `admin_token`
- `user_jwt_secret`
- `credential_encryption_key`
- `metrics_token`
- `grafana_admin_password`

另行创建以下两个文件：

- `postgres_dsn`：使用非回环域名，并包含 `sslmode=verify-full`。
- `redis_password`：Redis 独立密码。

Secret 目录必须为 `0700`，文件必须为普通文件且权限为 `0444`，以便只读挂载后由非 root 容器读取。不要把 Secret、`.env`、私钥或数据库密码提交到仓库。

### 2. 执行生产预检

```bash
export API_MANAGER_IMAGE='docker.io/dingding229/api-manager:latest'
export PROD_SECRETS_DIR='/absolute/path/api-manager-secrets'
export PROD_REDIS_ADDR='redis.example.com:6380'

python3 scripts/preflight-production.py
docker compose --env-file /dev/null \
  -f compose.production.yaml config -q
```

如启用镜像内完整观测栈，还需设置：

```bash
export PROD_OBSERVABILITY_STACK_ENABLED=true
export PROD_API_MEMORY_LIMIT=2g
```

### 3. 启动与验证

```bash
docker login
docker compose --env-file /dev/null \
  -f compose.production.yaml pull api-manager

docker compose --env-file /dev/null \
  -f compose.production.yaml up -d --force-recreate api-manager

python3 scripts/verify-production.py \
  --url http://127.0.0.1:8080 \
  --secret-dir "$PROD_SECRETS_DIR"
```

默认仅在宿主机回环地址发布 API Manager 的 8080 端口和 Grafana 的 3000 端口。

### 备份

停止写入后执行离线备份。`PGSERVICEFILE` 和 `PGPASSFILE` 必须指向仓库外的 libpq 配置文件：

```bash
docker compose --env-file /dev/null \
  -f compose.production.yaml stop api-manager

export PGSERVICE=api-manager
export PGSERVICEFILE='/absolute/path/pg_service.conf'
export PGPASSFILE='/absolute/path/.pgpass'

python3 scripts/backup-production.py \
  --output-dir /absolute/path/backups \
  --tool-image docker.io/dingding229/api-manager:latest
```

备份主机需要 Docker，以及与数据库版本兼容的 `pg_dump` 和 `pg_restore`。备份覆盖数据库及插件、插件库、观测数据三个 Docker 卷；可用 `--plugin-volume`、`--library-volume`、`--observability-volume` 覆盖卷名。备份必须加密保存，并定期执行恢复演练。

## Kubernetes / Helm

先创建命名空间及应用 Secret（`PROD_SECRETS_DIR` 指向前文的目录）：

```bash
kubectl create namespace api-manager
kubectl -n api-manager create secret generic api-manager-secrets \
  --from-file=ADMIN_TOKEN="$PROD_SECRETS_DIR/admin_token" \
  --from-file=USER_JWT_SECRET="$PROD_SECRETS_DIR/user_jwt_secret" \
  --from-file=CREDENTIAL_ENCRYPTION_KEY="$PROD_SECRETS_DIR/credential_encryption_key" \
  --from-file=POSTGRES_DSN="$PROD_SECRETS_DIR/postgres_dsn" \
  --from-file=REDIS_PASSWORD="$PROD_SECRETS_DIR/redis_password" \
  --from-file=METRICS_TOKEN="$PROD_SECRETS_DIR/metrics_token" \
  --from-file=GRAFANA_ADMIN_PASSWORD="$PROD_SECRETS_DIR/grafana_admin_password"
```

私有镜像仓库需要 Docker Hub 拉取凭据。在具备拉取权限的主机上执行 `docker login`，然后创建专用于 Kubernetes 的拉取 Secret；凭据文件必须包含 Docker Hub 凭据，不能仅含本机凭据助手的配置：

```bash
kubectl -n api-manager create secret generic dockerhub \
  --type=kubernetes.io/dockerconfigjson \
  --from-file=.dockerconfigjson=/absolute/path/dockerconfig.json
```

准备生产 values 文件，为 PostgreSQL、Redis、上游 API 和入口代理配置明确的 NetworkPolicy 放行规则。默认生产网络策略拒绝未声明的入站及出站流量；未放行依赖时应用不会就绪。

Chart 位于 `deploy/helm/api-manager`，默认使用 Docker Hub 的 `latest` 标签和 `Always` 拉取策略：

```bash
helm upgrade --install api-manager deploy/helm/api-manager \
  --namespace api-manager \
  --create-namespace \
  --set productionMode=true \
  --values /absolute/path/production-values.yaml \
  --set existingSecret=api-manager-secrets \
  --set 'imagePullSecrets[0].name=dockerhub' \
  --set env.REDIS_ADDR='redis.example.com:6380' \
  --set env.REDIS_TLS_ENABLED=true \
  --set image.repository=docker.io/dingding229/api-manager \
  --set image.tag=latest \
  --set image.pullPolicy=Always
```

`existingSecret` 必须包含以上生产 Secret。Chart 默认关闭 ServiceAccount Token 自动挂载、丢弃全部 Linux capabilities，并使用只读根文件系统。

镜像标签更新后执行以下命令触发重新拉取：

```bash
kubectl -n api-manager rollout restart deployment/api-manager-api-manager
kubectl -n api-manager rollout status deployment/api-manager-api-manager
```

生产环境需要根据入口网关、Prometheus 抓取器和外部服务配置 `ingress`、`networkPolicy`、`serviceMonitor` 与 `extraEgress`。生产模式要求启用 Redis TLS、持久化存储和外部 Secret。

Chart 使用单一持久卷中的 `plugins`、`plugin-library` 和 `observability` 独立子目录，并采用 `Recreate` 更新策略，避免 RWO 卷在更新期间发生多重挂载。

## JWT / HMAC 路由 Secret

API 路由的 JWT/HMAC `auth_config.secret_env` 只能引用 `API_AUTH_*` 命名空间，不能引用 `ADMIN_TOKEN`、数据库密码或其他平台 Secret。JWT 默认名称为：

```text
API_AUTH_JWT_HS256_SECRET
```

每个路由 Secret 必须至少 32 字节。应用支持两种来源：

- 环境变量：`API_AUTH_PARTNER_HMAC`
- 文件：`API_AUTH_PARTNER_HMAC_FILE=/run/secrets/API_AUTH_PARTNER_HMAC`

Helm 部署时，把对应 `API_AUTH_*` 键加入 `existingSecret`，并通过 `authSecretFiles` 声明文件映射：

```yaml
authSecretFiles:
  - API_AUTH_JWT_HS256_SECRET
  - API_AUTH_PARTNER_HMAC
```

文件型路由 Secret 在进程内缓存；轮换 Secret 后应滚动重启 API Manager。

HMAC 的时间戳、Nonce 和签名头仅用于网关鉴权，不会转发给上游；客户端的 `Authorization`、`Cookie`、`X-API-Key` 和管理鉴权头同样会在代理前移除。需要调用上游 API Key 时，应使用服务端托管并绑定目标 Origin 的 `upstream_auth_ref`。

## 常用配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | HTTP 监听地址 |
| `PRODUCTION_MODE` | `false` | 启用生产安全校验和失败关闭策略 |
| `ADMIN_TOKEN` / `ADMIN_TOKEN_FILE` | 无 | 首次管理员初始化令牌，至少 32 字符 |
| `USER_JWT_SECRET` / `USER_JWT_SECRET_FILE` | 无 | 控制台 JWT 密钥，至少 32 字符 |
| `USER_JWT_TTL` | `12h` | 控制台会话有效期，最大 24 小时 |
| `CREDENTIAL_ENCRYPTION_KEY` / `CREDENTIAL_ENCRYPTION_KEY_FILE` | 无 | 上游调用凭证加密密钥 |
| `POSTGRES_DSN` / `POSTGRES_DSN_FILE` | 无 | PostgreSQL URL；生产必须使用 `verify-full` |
| `USE_REDIS` | 根据 `REDIS_ADDR` 推断 | Redis 分布式限流开关 |
| `REDIS_ADDR` | `redis:6379` | Redis 主机和端口 |
| `REDIS_USERNAME` | 空 | Redis ACL 用户名 |
| `REDIS_PASSWORD` / `REDIS_PASSWORD_FILE` | 空 | Redis 密码 |
| `REDIS_TLS_ENABLED` | `false` | Redis TLS，使用系统信任根 |
| `METRICS_TOKEN` / `METRICS_TOKEN_FILE` | 无 | 生产 `/metrics` Bearer Token |
| `MAX_BODY_BYTES` | `1048576` | 普通请求体上限 |
| `PLUGIN_MAX_BYTES` | `20971520` | 插件上传上限 |
| `CORS_ORIGINS` | 空 | 允许的浏览器 Origin；生产禁止 `*` |
| `PLUGIN_DIR` | `plugins` | 插件持久化目录 |
| `PLUGIN_LIBRARY_DIR` | `plugin-library` | 插件库持久化目录 |
| `OBSERVABILITY_STACK_ENABLED` | `false` | 启动镜像内六组件观测栈 |
| `GRAFANA_ADMIN_PASSWORD_FILE` | 空 | 完整观测栈的 Grafana 管理密码 |
| `OBSERVABILITY_DIR` | `data/observability` | 内置观测数据目录 |
| `OTEL_ENABLED` | `false` | 向外部 OTLP 接收端额外导出 Trace |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | 空 | OTLP gRPC `host:port` |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` | 明文 OTLP；生产只允许镜像内回环链路 |

非法布尔值、整数、Duration、日志级别或越界资源配置会使进程启动失败，不会静默回退。

## 健康检查与安全要求

- `/health/live`：存活检查。
- `/health/ready`：PostgreSQL、Redis 和运行依赖就绪检查。
- `/metrics`：生产环境要求 `Authorization: Bearer <METRICS_TOKEN>`。
- 生产 API 上游只允许 HTTPS，并执行 DNS/IP 校验以阻止回环、链路本地、元数据和其他特殊地址访问；私有地址必须通过绑定目标 Origin 的服务端上游凭证显式批准。
- 加密密钥必须长期保存；丢失或更换后，已有上游调用凭证无法再次解密。
- 单容器六组件模式和单副本插件注册表不适合作为高可用观测平台。
- 上线前必须完成实际环境的 PostgreSQL/Redis TLS 联调、备份恢复演练、容量测试和入口层 HTTPS 验证。
