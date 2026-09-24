# API Manager

一个面向公用 API 接口的可私有化部署管理平台框架，使用 Go 编写，当前已经具备可运行的控制面、数据面、持久化、鉴权、限流、反向代理、发布版本和插件基础能力。

## 已实现能力

- Go HTTP 服务和优雅退出
- PostgreSQL 持久化以及启动时自动迁移
- 内存存储降级模式
- Redis 固定窗口分布式限流
- 内存限流开发模式
- API 创建、查询、更新、删除
- API 发布、下线、发布快照和回滚
- 精确路由和 `{id}` 路径参数路由匹配
- 静态 JSON 响应
- HTTP/HTTPS 上游反向代理
- API Key 鉴权
- JWT HS256 鉴权
- HMAC 请求签名鉴权
- 每分钟限流、每日配额、每月配额
- 用户初始化、登录和 JWT 会话
- 用户列表及管理员创建用户
- 内置 `echo` 插件
- WASM/wazero 插件加载框架
- JSON 结构化日志
- Request ID
- 基础安全响应头和 CORS
- OpenAPI 3.0 文档导出
- Prometheus 指标占位接口
- Loki、Alloy、Prometheus、Grafana Docker 组件
- 单元测试、集成测试和 Race Detector 测试

## 快速启动：内存开发模式

```bash
cd /Volumes/SSD/项目/api
make run
```

启动前需设置三个相互独立的随机密钥（至少 32 字符）。未提供或使用示例值时程序会拒绝启动：

```bash
export ADMIN_TOKEN="$(openssl rand -hex 32)"
export USER_JWT_SECRET="$(openssl rand -hex 32)"
export CREDENTIAL_ENCRYPTION_KEY="$(openssl rand -hex 32)"
make run
```
内存模式不持久化；Docker Compose 才使用 PostgreSQL。

## Docker Compose 启动

Docker Compose 会启动：

- API Manager
- PostgreSQL
- Redis
- Loki
- Grafana Alloy
- Prometheus
- Grafana

```bash
cd /Volumes/SSD/项目/api
# 仅供空数据库的新安装；现有实例须按后文“安全升级”迁移
python3 scripts/init-secrets.py
docker compose up --build
```

Compose 模式会自动使用 PostgreSQL 和 Redis，并在服务启动时执行数据库迁移。

默认地址：

```text
API Manager： http://localhost:8080
Grafana：     http://localhost:3000
Prometheus：  http://localhost:9090
Loki：        http://localhost:3100
Alloy：       http://localhost:12345
```

Grafana 账号默认 `admin`，密码必须通过 `GRAFANA_ADMIN_PASSWORD` 明确设置；监控端口仅绑定本机。

## 健康检查

```bash
curl http://localhost:8080/health/live
curl http://localhost:8080/health/ready
curl http://localhost:8080/metrics
```

`/health/ready` 会同时检查 PostgreSQL 和 Redis；内存模式下直接返回 ready。

## 创建 API Key

```bash
curl -X POST http://localhost:8080/admin/v1/credentials \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"local-client"}'
```

响应中的 `api_key` 在创建时返回，并使用独立的 `CREDENTIAL_ENCRYPTION_KEY` 加密存储以供拥有 `credential.reveal` 权限的管理员再次查看；鉴权使用 SHA-256 哈希。更换加密密钥前需运行密钥迁移工具。

## 创建静态响应 API

```bash
curl -X POST http://localhost:8080/admin/v1/apis \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name":"Hello API",
    "method":"GET",
    "path":"/api/hello",
    "auth_mode":"api_key",
    "rate_limit_per_minute":60,
    "daily_quota":10000,
    "monthly_quota":300000,
    "response_body":"{\"message\":\"hello\"}"
  }'
```

返回 API 的 `id` 后发布：

```bash
curl -X POST http://localhost:8080/admin/v1/apis/API_ID/publish \
  -H "X-Admin-Token: $ADMIN_TOKEN"
```

调用：

```bash
curl http://localhost:8080/api/hello -H 'X-API-Key: YOUR_API_KEY'
```

## 创建上游代理 API

```bash
curl -X POST http://localhost:8080/admin/v1/apis \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name":"Upstream API",
    "method":"GET",
    "path":"/api/upstream",
    "upstream_url":"https://httpbin.org",
    "upstream_path":"/get"
  }'
```

也可以使用路径参数：

```json
{
  "name": "User API",
  "method": "GET",
  "path": "/api/users/{id}",
  "upstream_url": "https://example.com"
}
```

当前路径参数用于路由匹配，后续可继续增加参数提取、重写和校验能力。

## JWT 鉴权

API 配置：

```json
{
  "auth_mode": "jwt",
  "auth_config": {
    "secret_env": "JWT_HS256_SECRET",
    "issuer": "example",
    "audience": "api-users"
  }
}
```

启动服务前设置：

```bash
export JWT_HS256_SECRET='replace-with-jwt-secret'
```

请求：

```bash
curl http://localhost:8080/api/hello \
  -H 'Authorization: Bearer YOUR_JWT'
```

## HMAC 鉴权

API 配置：

```json
{
  "auth_mode": "hmac",
  "auth_config": {
    "secret_env": "PAYMENT_API_SECRET",
    "timestamp_header": "X-Timestamp",
    "signature_header": "X-Signature"
  }
}
```

签名原文为：

```text
timestamp + "\\n" + nonce + "\\n" + HTTP_METHOD + "\\n" + request_uri + "\\n" + hex(sha256(body))
```

签名算法为 HMAC-SHA256，时间戳允许偏差为 300 秒。每次请求必须携带唯一 `X-Nonce`（16–128 位安全字符），请求体上限 1 MiB；签名绑定完整请求体，同一个 nonce 在 10 分钟内只能使用一次。多副本部署应使用 Redis 限流器共享 nonce。

## 用户系统

如果设置了 `USER_JWT_SECRET`，可以初始化第一个超级管理员：

```bash
curl -X POST http://localhost:8080/auth/v1/bootstrap \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"password123"}'
```

登录：

```bash
curl -X POST http://localhost:8080/auth/v1/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"password123"}'
```

使用返回的 JWT 访问管理端：

```bash
curl http://localhost:8080/admin/v1/users \
  -H 'Authorization: Bearer USER_JWT'
```

管理员创建普通用户：

```bash
curl -X POST http://localhost:8080/admin/v1/users \
  -H 'Authorization: Bearer USER_JWT' \
  -H 'Content-Type: application/json' \
  -d '{"email":"developer@example.com","password":"password123","role":"api_developer"}'
```

当前角色包括：

```text
super_admin
tenant_admin
operator
api_developer
viewer
```

## 发布、版本和回滚

发布 API 时会生成不可变发布快照：

```bash
POST /admin/v1/apis/{id}/publish
```

查询发布历史：

```bash
GET /admin/v1/apis/{id}/releases
GET /admin/v1/apis/{id}/releases/{version}
```

回滚：

```bash
POST /admin/v1/apis/{id}/rollback/{version}
```

## 插件系统

### 内置插件

当前内置 `echo` 插件：

```json
{
  "name": "Echo API",
  "method": "GET",
  "path": "/api/echo",
  "plugin": "echo"
}
```

### WASM 插件

插件目录格式：

```text
plugins/my-plugin/
├── manifest.yaml
└── plugin.wasm
```

`manifest.yaml`：

```yaml
id: dev.example.hello
name: hello
version: 0.1.0
api_version: v1
runtime: wasm
entrypoint: plugin.wasm
limits:
  timeout_ms: 3000
  memory_mb: 32
routes:
  - name: 服务状态
    method: GET
    path: /api/example/v1/status
    auth_mode: api_key
```

`routes` 仅声明插件能力并供控制台生成调用示例，不会自动创建或发布接口。管理员仍需确认接口配置并发布。未声明路由的插件不会被控制台猜测为 `/v1/status`。

WASM ABI：

```text
导出 memory
导出 alloc(size i32) -> i32
导出 handle(request_ptr i32, request_len i32) -> i64
```

`handle` 返回值的高 32 位为响应指针，低 32 位为响应长度。请求和响应均为 JSON，body 使用 Base64 编码。

WASM 插件默认不开放宿主文件系统、环境变量和网络导入，并且具备执行超时和内存上限。

## 管理接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/admin/v1/apis` | 查询 API 列表 |
| POST | `/admin/v1/apis` | 创建 API |
| GET | `/admin/v1/apis/{id}` | 查询 API |
| PUT | `/admin/v1/apis/{id}` | 更新 API |
| DELETE | `/admin/v1/apis/{id}` | 删除 API |
| POST | `/admin/v1/apis/{id}/publish` | 发布 API |
| POST | `/admin/v1/apis/{id}/unpublish` | 下线 API |
| GET | `/admin/v1/apis/{id}/releases` | 查询发布历史 |
| GET | `/admin/v1/apis/{id}/releases/{version}` | 查询发布快照 |
| POST | `/admin/v1/apis/{id}/rollback/{version}` | 回滚 API |
| GET | `/admin/v1/credentials` | 查询凭证列表 |
| POST | `/admin/v1/credentials` | 创建 API Key |
| POST | `/admin/v1/credentials/{id}/revoke` | 撤销 API Key |
| GET | `/admin/v1/plugins` | 查询已注册插件 |
| GET | `/admin/v1/users` | 查询用户 |
| POST | `/admin/v1/users` | 创建用户 |
| GET | `/admin/v1/openapi.json` | 导出 OpenAPI |

管理接口支持：

```text
X-Admin-Token: ADMIN_TOKEN
Authorization: Bearer USER_JWT
```

## 数据库迁移

迁移文件位于：

```text
migrations/
```

当前包括：

```text
000001_initial.up.sql
000002_gateway_fields.up.sql
000003_users.up.sql
```

PostgreSQL 模式启动时会自动执行内置迁移。运行时仓储通过以下接口抽象：

```text
internal/store/store.go
```

## 日志和监控

API Manager 输出 JSON 日志到标准输出，Alloy 从 Docker 日志流采集并写入 Loki。Grafana 自动配置 Loki 和 Prometheus 数据源。

日志中包含：

- Request ID
- API ID
- HTTP 方法和路径
- 响应状态码
- 响应字节数
- API Key 不包含原文
- 插件和上游名称
- 请求耗时

## 测试

```bash
make test
make lint
go test -race ./...
docker compose config
```

Docker 镜像实际构建需要本机 Docker daemon 正常运行：

```bash
docker build -t api-manager:local .
```

## 当前仍可继续增强的部分

以下是下一阶段重点，而不是当前框架的阻塞项：

- 完整租户隔离以及跨租户资源授权
- OAuth 2.0、JWKS 和 mTLS
- 请求参数和响应 JSON Schema 校验
- 上游负载均衡、熔断和健康检查
- 插件库完善、数据库化插件管理和更丰富的插件生态
- OpenTelemetry Trace/Metric 导出
- Grafana Dashboard 和告警规则
- Kubernetes Helm 部署
- 多数据面实例的配置推送和热更新

## 管理 Web 控制台与 RBAC

浏览器控制台入口：

```text
http://localhost:8080/console/
```

首次部署时，先使用 `ADMIN_TOKEN` 初始化超级管理员，然后使用账号密码登录。控制台会调用 `/auth/v1/me` 获取当前用户权限，并隐藏无权限的导航和操作按钮；所有权限限制仍由后端强制执行。

RBAC 内置权限：

```text
api.read
api.write
api.publish
api.delete
credential.read
credential.write
plugin.read
plugin.manage
user.read
user.manage
audit.read
```

内置角色：

```text
super_admin
tenant_admin
operator
api_developer
viewer
```

新增 RBAC 管理接口：

| 方法 | 路径 | 权限 |
|---|---|---|
| GET | `/admin/v1/permissions` | `user.read` |
| GET | `/admin/v1/roles` | `user.read` |
| POST | `/admin/v1/roles` | `user.manage` |
| PUT | `/admin/v1/roles/{name}/permissions` | `user.manage` |
| PUT | `/admin/v1/users/{id}/roles` | `user.manage` |
| PUT | `/admin/v1/users/{id}/status` | `user.manage` |

管理员 Token 作为紧急运维凭证可绕过 RBAC；常规管理操作应使用用户 JWT。生产环境应保管好 `ADMIN_TOKEN` 并限制其可访问范围。

## 审计日志

所有成功的高风险管理变更都会写入 PostgreSQL 的 `audit_logs` 表；本地内存模式也保留相同行为，便于开发和自动化测试。审计记录包含操作时间、操作者类型/用户、资源标识、请求 ID、HTTP 方法、来源 IP、User-Agent、响应状态及经过脱敏处理的业务详情。

已覆盖的操作包括：

- 接口创建、更新、发布、下线、回滚与删除
- 调用凭证创建与撤销（**不会**记录 API Key 原文或哈希）
- 用户初始化、登录、创建、状态变更与角色变更
- 角色创建与角色权限变更

查询接口需要 `audit.read` 权限：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/admin/v1/audit-logs` | 分页查询审计记录 |

支持的查询参数：

| 参数 | 说明 |
|---|---|
| `page` | 页码，默认 `1` |
| `page_size` | 每页记录数，默认 `20`，最大 `100` |
| `action` | 精确操作名，例如 `api.publish` |
| `resource_type` | 资源类型，例如 `api`、`credential`、`user`、`role` |
| `actor_id` | 操作者用户 UUID |
| `request_id` | 请求关联 ID（`X-Request-ID`） |
| `from` / `to` | RFC 3339 时间范围，例如 `2026-09-21T00:00:00Z` |

示例：

```bash
curl 'http://localhost:8080/admin/v1/audit-logs?action=api.publish&page=1&page_size=20' \
  -H 'Authorization: Bearer USER_JWT'
```

控制台的“**审计日志**”菜单会提供时间、操作、资源、操作者和请求 ID 筛选，以及详情查看和分页。敏感字段（密码、密钥、令牌、认证头、哈希等）在写入前会统一替换为 `[REDACTED]`。

## OpenAPI 导入与 JSON Schema 校验

接口现在支持为请求体和响应体配置 JSON Schema。Schema 会在接口创建/更新时编译校验；已发布接口请求进入网关后会先校验请求 JSON，响应返回前会校验响应 JSON。校验失败时分别返回 `400` 和 `502`。

接口管理请求示例：

```json
{
  "name": "创建订单",
  "method": "POST",
  "path": "/api/orders",
  "auth_mode": "none",
  "upstream_url": "https://orders.example.com",
  "request_schema": {
    "type": "object",
    "required": ["id"],
    "properties": {
      "id": {"type": "integer"}
    },
    "additionalProperties": false
  },
  "response_schema": {
    "type": "object",
    "required": ["order_id"],
    "properties": {
      "order_id": {"type": "string"}
    }
  }
}
```

新增 OpenAPI 导入接口：

```text
POST /admin/v1/openapi/import
```

请求格式：

```json
{
  "document": {"openapi": "3.0.3", "paths": {}},
  "upstream_url": "https://api.example.com",
  "path_prefix": "/api"
}
```

说明：

- 当前支持 OpenAPI 3.x JSON 文档。
- 默认使用文档第一个 `servers[].url` 作为上游地址，也可以通过 `upstream_url` 覆盖。
- 每个 `path + operation` 会创建为一个未发布草稿。
- 会导入 `requestBody.application/json.schema` 和成功响应中的 JSON Schema。
- 支持解析 `#/components/...` 本地引用。
- 导入结果会返回成功创建列表和失败列表。
- 控制台“接口管理”页面提供 OpenAPI 文档导入表单和 JSON Schema 配置项。

### WASM 插件生命周期管理

插件管理现已支持数据库登记、版本管理、上传、启用、禁用和删除。

新增接口：

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/admin/v1/plugins` | `plugin.read` | 查询运行时插件和托管版本 |
| POST | `/admin/v1/plugins` | `plugin.manage` | multipart 上传 `manifest` 和 `wasm` |
| PUT | `/admin/v1/plugins/{id}/status` | `plugin.manage` | 启用或禁用插件版本 |
| DELETE | `/admin/v1/plugins/{id}` | `plugin.manage` | 删除已禁用插件版本 |

上传示例：

```bash
curl -X POST http://localhost:8080/admin/v1/plugins \
  -H 'X-Admin-Token: ADMIN_TOKEN' \
  -F 'manifest=@manifest.yaml' \
  -F 'wasm=@plugin.wasm' \
```

插件上传只需要 `manifest.yaml` 和 WASM 模块。系统会在上传、启用和启动加载时校验清单、文件完整性与 WASM ABI；插件默认禁用，启用后还需创建并发布 API 路由。详见 [独立插件开发文档](docs/WASM接口插件独立开发文档.md)。

本地插件库由 `PLUGIN_LIBRARY_DIR` 指定，Docker 卷 `plugin_library` 持久化。可在控制台发布已验签的已安装版本并从库安装；管理 API 为 `GET/POST /admin/v1/plugin-library` 和 `POST /admin/v1/plugin-library/install?name=...&version=...`，权限分别为 `plugin.read`/`plugin.manage`。这是本地缓存而非远程插件市场，库安装后仍需启用并发布接口。

已预留 `plugin_data` 的按插件命名空间 JSON 存储和 `DatabaseWriter` 主机侧接口；`PLUGIN_DATABASE_WRITES_ENABLED` 默认关闭，声明 `capabilities: [database_write]` 的插件无法启用。**WASM host ABI 尚未暴露数据库操作，开启该开关目前并不赋予实际写入能力。**

插件上传后默认禁用，启用时会再次验证 WASM ABI，并将模块加载到运行时。当前同一插件名称最多启用一个版本；启用新版本会自动停用同名旧版本。删除前必须先禁用插件。

插件包默认保存到 `PLUGIN_DIR`，Docker Compose 使用 `plugin_data` 卷持久化插件文件。上传大小由 `PLUGIN_MAX_BYTES` 控制，默认 20 MiB。插件必须满足：

- `runtime: wasm`
- `version` 非空
- `entrypoint` 为当前目录下的 `.wasm` 文件名
- 导出 `memory`
- 导出 `alloc(i32) -> i32`
- 导出 `handle(i32, i32) -> i64`

服务启动时只自动加载数据库中处于启用状态的托管插件。插件上传、启用、禁用和删除操作都会进入审计日志。

## 可观测性：OpenTelemetry、Prometheus、Grafana 与告警

### 分布式链路追踪

服务提供 OpenTelemetry TraceContext 传播：入口请求会创建服务端 Span，反向代理上游请求会创建客户端 Span 并向上游传播 `traceparent`。结构化 HTTP 日志增加 `trace_id` 和 `span_id`，可以通过 Request ID 与审计日志关联排障。

相关环境变量：

```text
OTEL_ENABLED=false
OTEL_SERVICE_NAME=api-manager
OTEL_EXPORTER_OTLP_ENDPOINT=tempo:4317
OTEL_EXPORTER_OTLP_INSECURE=true
```

Docker Compose 默认启用追踪，并将 OTLP/gRPC Trace 导出到 Tempo。Tempo 查询入口为 `http://localhost:3200`，Grafana 中已自动配置 Tempo 数据源。

### Prometheus 指标

`GET /metrics` 现在输出以下主要指标：

| 指标 | 说明 |
|---|---|
| `api_manager_http_requests_total` | 按管理面、认证、控制台、网关路由和状态码统计的 HTTP 请求数 |
| `api_manager_gateway_requests_total` | 按请求方法和状态码统计的网关请求数 |
| `api_manager_gateway_errors_total` | 网关 4xx / 5xx 错误计数 |
| `api_manager_gateway_request_duration_seconds` | 网关请求延迟直方图，可计算 p95 / p99 |
| `api_manager_gateway_rate_limit_hits_total` | 限流拒绝数 |
| `api_manager_gateway_auth_failures_total` | 调用鉴权失败数 |
| `api_manager_gateway_schema_validation_failures_total` | 请求或响应 Schema 校验失败数 |
| `api_manager_gateway_upstream_failures_total` | 上游代理失败数 |
| `api_manager_gateway_plugin_failures_total` | 插件执行失败数 |
| `api_manager_gateway_upstream_retries_total` | 上游重试次数 |
| `api_manager_gateway_circuit_breaker_rejections_total` | 因熔断器拒绝的请求数 |
| `api_manager_http_request_duration_seconds` | 按服务面与 HTTP 方法统计的请求延迟直方图 |
| `api_manager_http_inflight_requests` | 当前处理中的 HTTP 请求数 |
| `api_manager_process_uptime_seconds` | 进程运行时长 |

### Grafana 仪表盘与告警

新增预置 Grafana Dashboard：

```text
API Manager / API Manager Overview
```

仪表盘包括：请求速率、网关错误率、p95 延迟、限流拒绝、鉴权/Schema/上游异常、在途请求与运行时长。

Prometheus 告警规则位于：

```text
/Volumes/SSD/项目/api/configs/prometheus/alerts.yml
```

默认规则：

- `APIManagerDown`：两分钟无法抓取服务
- `APIManagerHighErrorRate`：五分钟网关错误率高于 5%
- `APIManagerHighP95Latency`：十分钟 p95 延迟高于 1 秒
- `APIManagerRateLimitSpike`：十分钟限流拒绝速率高于每秒 10 次
- `APIManagerCircuitBreakerOpen`：连续五分钟存在上游熔断拒绝

> 生产部署（反向代理由运维自行提供）请先阅读 [生产部署技术基线](docs/生产部署技术基线.md)。本地 Compose 不可直接用于生产。

## 生产化与平台部署补充

### 上游服务治理与参数契约

每个代理 API 都可在创建或更新请求中设置：

```json
{
  "upstream_timeout_ms": 5000,
  "upstream_retries": 2,
  "circuit_breaker_threshold": 5,
  "circuit_breaker_reset_seconds": 30,
  "parameters_schema": {
    "type": "object",
    "properties": {
      "path": {"type": "object"},
      "query": {"type": "object"},
      "header": {"type": "object"}
    }
  }
}
```

- 超时覆盖单个上游请求；`0` 表示沿用服务端默认行为。
- 仅 `GET`、`HEAD`、`OPTIONS`、`PUT`、`DELETE` 等可安全重放的请求才会重试；带请求体的请求必须可重放。
- 连续失败达到阈值后断路器会立即拒绝后续请求，等待恢复时间后放行一个探测请求。
- 参数 Schema 的实例为 `{ "path": {}, "query": {}, "header": {} }`；路径参数、查询参数和首个 Header 值均以字符串提供。

### 验证、告警与压测

```bash
make validate
# Docker Compose 已启动后
make verify-stack
# 需要自行预先创建并发布一个测试 API
k6 run -e API_MANAGER_URL=http://localhost:8080 -e API_PATH=/api/health tests/k6/gateway.js
```

Prometheus 通过 Alertmanager 路由告警。默认配置不发送外部消息，以防本地环境误发；生产环境请用私有覆盖文件为 Slack、邮件、Teams、钉钉或 Webhook 配置 receiver。

### Kubernetes / Helm

```bash
helm lint deploy/helm/api-manager
helm template api-manager deploy/helm/api-manager -f production-values.yaml
helm upgrade --install api-manager deploy/helm/api-manager -n api-manager --create-namespace -f production-values.yaml
```

Chart 包含 Deployment、Service、Ingress（可选）、PVC、HPA、PDB、NetworkPolicy 和 ServiceMonitor（可选）。数据库、Redis 及可观测性组件应使用受管服务或独立的生产级 Chart，并通过私有 values 文件注入连接信息与机密。

## 接入 Game Discount API

另提供可上传测试的 [Game Discount WASM 示例插件](integrations/game-discount/wasm/README.md)，不替代原有上游服务或实时数据库。

已为 `/Volumes/SSD/项目/game-discount-api` 提供独立上游服务接入：

- `GET /api/game-discount/v1/offers?region=HK`
- `GET /api/game-discount/v1/offers/{external_id}`
- `GET /api/game-discount/v1/status`

详见 `integrations/game-discount/README.md`。使用 `make game-discount-up` 启动组合 Compose，服务就绪后 `make game-discount-register` 注册并发布。`make game-discount-test` 可执行不依赖 Docker 的双服务真实 HTTP 验证。

## 插件开发

- [WASM 接口插件独立开发文档](docs/WASM接口插件独立开发文档.md)
- [WASM 插件开发文档（完整参考）](docs/插件开发文档.md)

## 2026-09 安全升级与已有环境迁移

- **不会自动替换当前运行容器的密钥，也不会自动重启服务。** 在容器重建前，先备份数据库并规划维护窗口。
- 当前旧环境若使用公开默认 `ADMIN_TOKEN` / `USER_JWT_SECRET`，必须手工轮换，并撤销旧的登录会话及旧的调用凭证。不得把真实密钥提交到仓库。
- 如果旧 API Key 是使用旧 Admin Token 回退加密的，保留旧值仅用于离线迁移，在停止网关并备份数据库后使用 `OLD_CREDENTIAL_ENCRYPTION_KEY`（旧 Admin Token）、`CREDENTIAL_ENCRYPTION_KEY`（新独立密钥）、`POSTGRES_DSN` 运行 `go run ./cmd/rotate-credential-encryption`；迁移成功后撤掉旧密钥。无法解密的历史凭证应轮换，不能假装可恢复。
- PostgreSQL 已初始化的数据卷不会因为 Compose `POSTGRES_PASSWORD` 变化而自动更改数据库用户口令；应先在 PostgreSQL 中安全执行 `ALTER ROLE` 并更新 DSN，然后再重启网关。
- HMAC 签名格式已经更新，旧签名客户端必须同时升级。JWT 路由必须配置 `issuer` 和 `audience`，Token 必须含 `exp` 与 `iat`。
- 只有服务端显式配置 `API_UPSTREAM_CREDENTIALS` 并在接口上绑定匹配的 `upstream_auth_ref`，才允许代理到内网地址；其他目标需是可公开路由的 IP。
- Helm 默认单副本并要求从 Secret 注入 `POSTGRES_DSN` 和三种独立密钥；插件库使用 PVC。多副本插件状态广播、完全多租户隔离、事务化审计仍需架构演进，不能以此版本宣称已经支持。

## 私有仓库 CI/CD

源码托管在私有 GitHub 仓库，容器镜像发布到私有 Docker Hub 仓库 `dingding229/api-manager`。

- Pull Request：执行 Go 竞态测试、静态检查、Web 控制台测试、生产脚本测试、Compose/Helm 校验以及 Trivy 源码扫描，不发布镜像。
- `main` 分支：所有检查通过后，构建并扫描镜像，再发布 `latest` 与 `sha-<commit>` 标签。
- `v*` Git 标签：额外发布语义化版本标签。
- 发布镜像同时生成 SBOM 和 provenance，并构建 `linux/amd64`、`linux/arm64` 两个平台。
- GitHub Actions 使用仓库 Secret `DOCKERHUB_USERNAME` 和 `DOCKERHUB_TOKEN` 登录；严禁把凭证写入 Workflow、Compose 或源码。

私有镜像部署前应先登录，并优先使用 Actions 输出的不可变 digest：

```bash
docker login
docker pull dingding229/api-manager@sha256:<GitHub Actions 输出的摘要>
```

生产 Compose 的 `API_MANAGER_IMAGE` 也必须使用该摘要，不能只使用 `latest`。
