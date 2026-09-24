# 公用 API 接口管理程序开发规划

> 文档版本：v0.1
> 更新日期：2026-09-21
> 文档状态：规划稿

## 一、项目定位

建设一个可私有化部署的 API 管理与运行平台，统一完成：

- API 接口注册、发布、下线和版本管理
- 通过插件动态增加新接口和处理逻辑
- API Key、JWT、HMAC 等调用鉴权
- 限流、配额、黑白名单、超时、重试等流量治理
- 请求日志、操作审计、指标和链路追踪
- 预留用户、租户和权限体系
- Docker 一键部署
- 后续平滑扩展到多实例、高可用部署

系统在逻辑上拆分为两个部分：

1. **控制面（Control Plane）**：管理接口、插件、密钥、策略、日志查询等。
2. **数据面（Data Plane）**：实际接收 API 请求，执行鉴权、限流和插件调用。

第一版可以将两者编译进同一个程序，通过启动参数决定运行模式，避免过早微服务化。

---

## 二、技术选型

### 1. 后端语言：Go

建议使用 Go 稳定版本线。

选择原因：

- 编译后是单个二进制文件，Docker 镜像可以做得很小
- 并发模型适合 API 网关和高并发网络服务
- 标准库的 HTTP、TLS、JSON 和性能分析能力完善
- 跨平台编译和部署方便
- 适合同时实现控制面和高性能数据面

### 2. 建议技术栈

| 模块 | 技术选择 |
|---|---|
| 后端语言 | Go |
| HTTP 路由 | `net/http` + `chi`，或直接封装标准库路由 |
| 管理接口 | RESTful API |
| 配置数据库 | PostgreSQL |
| 限流、配额和缓存 | Redis |
| 插件运行时 | WebAssembly + wazero |
| 外部插件协议 | gRPC，可作为第二阶段扩展 |
| 配置格式 | YAML + 环境变量 |
| API 描述 | OpenAPI 3.x |
| 结构化日志 | Go `slog` JSON |
| 可观测性标准 | OpenTelemetry |
| 日志系统 | Grafana Loki + Grafana Alloy + Grafana |
| 指标系统 | Prometheus |
| 链路追踪 | Tempo，可作为增强组件 |
| 数据库迁移 | Goose 或 Atlas |
| 容器部署 | Docker + Docker Compose |
| 管理界面 | 第一阶段 REST API；第二阶段增加轻量 Web 控制台 |

---

## 三、总体架构

```text
                       ┌─────────────────────┐
                       │    管理员/运维人员    │
                       └──────────┬──────────┘
                                  │
                          Admin REST API
                                  │
                 ┌────────────────▼────────────────┐
                 │           控制面 Control Plane   │
                 │                                │
                 │ API管理 / 插件管理 / 密钥管理    │
                 │ 策略管理 / 用户预留 / 审计日志   │
                 └───────────┬───────────┬────────┘
                             │           │
                       PostgreSQL      Redis
                             │           │
                 ┌───────────▼───────────▼────────┐
客户端请求 ───────▶│           数据面 Data Plane     │
                 │                                │
                 │ 路由匹配                       │
                 │ 调用鉴权                       │
                 │ 限流与配额                     │
                 │ 请求校验                       │
                 │ 插件执行                       │
                 │ 上游代理                       │
                 │ 响应处理                       │
                 └───────────┬───────────┬────────┘
                             │           │
                         WASM插件      上游API
                             │
                      JSON结构化日志
                             │
                    Grafana Alloy
                             │
                         Loki
                             │
                        Grafana
```

---

## 四、核心功能模块

### 1. API 管理模块

第一版需要实现：

- API 创建、修改、复制、删除
- API 分组
- HTTP 方法管理
- 路径及路径参数配置
- Query、Header、Body 参数定义
- 请求和响应 Schema
- 上游地址配置
- 静态响应配置
- 插件处理器绑定
- 接口启用和停用
- 接口版本管理
- 草稿、发布、下线状态
- 配置发布和历史版本回滚
- OpenAPI 文档导入
- OpenAPI 文档导出
- 接口调试功能
- 健康检查

建议的数据模型：

```text
apis
api_versions
api_routes
api_upstreams
api_schemas
api_policies
api_releases
environments
```

建议将“编辑中的配置”和“数据面正在运行的配置”分离。管理员修改配置后，需要执行一次“发布”，数据面再加载一个不可变的配置快照，方便回滚和审计。

### 2. 调用鉴权模块

#### 第一阶段支持

- 无鉴权
- API Key
  - Header 传递
  - Query 参数传递
- JWT
  - 本地密钥校验
  - JWKS 公钥校验
- HMAC 请求签名
  - 时间戳
  - Nonce
  - 请求体摘要
  - 防重放
- IP 白名单
- IP 黑名单

#### 第二阶段支持

- OAuth 2.0 Client Credentials
- mTLS 客户端证书
- 外部鉴权服务
- 自定义鉴权插件

#### 密钥安全设计

- API Key 只在创建时显示一次
- 数据库只保存 Key 前缀和哈希
- HMAC Secret 加密保存
- JWT 私钥不直接写入普通配置文件
- 支持密钥有效期、禁用和轮换
- 每个调用凭证可以绑定 API、API 分组、环境、权限范围、限流策略和调用配额

### 3. 请求限制与流量治理

#### 第一版功能

- 单次请求体大小限制
- Header 数量及大小限制
- 请求超时
- 上游连接超时
- 上游响应超时
- 最大并发数
- 每秒/每分钟请求限制
- 每日、每月调用配额
- 请求方法限制
- Content-Type 限制
- CORS
- IP 黑白名单
- 参数和 JSON Schema 校验

#### 限流维度

支持按以下维度组合限流：

- 全局
- API
- 路由
- API Key
- 用户
- 租户
- IP
- 插件
- 自定义 Header

#### 限流算法

建议第一版实现：

- Token Bucket：处理持续流量和突发流量
- Fixed Window：处理日/月配额
- Redis Lua 脚本：保证多实例部署下的原子性

第二阶段可以增加：

- Sliding Window
- 并发请求数限制
- 上游自适应限流
- 熔断和半开恢复
- 请求排队

### 4. 上游代理模块

需要包括：

- HTTP/HTTPS 上游
- 多上游节点
- 轮询负载均衡
- 权重负载均衡
- 主备上游
- 上游健康检查
- Header 添加、删除和修改
- Query 参数转换
- 路径重写
- 超时配置
- 有条件重试
- 熔断
- 响应缓存
- 响应压缩
- 敏感 Header 清理

只有在请求具备幂等性，或者明确配置了幂等键时，才对 POST 等请求进行自动重试。

---

## 五、插件系统设计

### 1. 插件方案

建议采用两级插件机制。

#### 主要插件机制：WebAssembly

使用 wazero 在 Go 进程中执行 WASM 插件：

- 插件可以动态安装和卸载
- 插件与主程序之间有隔离边界
- 可以限制执行时间和内存
- 可以跨语言编写插件
- 不需要在容器内安装额外运行时
- 插件崩溃不应直接导致主进程崩溃

#### 扩展插件机制：独立进程/gRPC

对于以下场景，使用独立进程插件：

- 依赖重量级 SDK
- 需要完整网络访问
- 需要运行 Python、Java 或 Node.js 生态代码
- 插件逻辑复杂
- 插件由第三方团队独立部署
- 插件需要单独扩缩容

#### 不建议直接使用 Go `.so` 插件

Go 原生 `plugin` 包存在平台支持、编译器版本、构建参数和依赖版本必须完全一致等限制，在容器化和第三方插件场景中维护成本较高。可以优先使用 WASM，或使用 RPC 进行进程间通信。

### 2. 插件类型

#### Handler 插件

插件本身提供一个新 API：

```text
POST /api/weather/query
GET  /api/exchange-rate
POST /api/document/convert
```

插件负责生成最终响应。

#### Middleware 插件

在请求处理链中执行：

- 自定义鉴权
- 请求参数转换
- Header 处理
- 数据脱敏
- 响应转换
- 内容审核
- 签名计算
- 缓存控制

#### Connector 插件

负责访问特定第三方服务：

- 云服务接口
- 支付平台
- 短信服务
- 企业内部系统
- 数据库查询服务

### 3. 插件生命周期

建议定义以下钩子：

```text
Init
ValidateConfig
OnEnable
BeforeRequest
HandleRequest
AfterResponse
OnError
HealthCheck
OnDisable
Shutdown
```

请求执行顺序：

```text
路由匹配
  → 请求ID生成
  → 鉴权
  → 限流
  → 参数校验
  → BeforeRequest插件
  → Handler插件或上游代理
  → AfterResponse插件
  → 访问日志
  → 返回客户端
```

### 4. 插件包格式

```text
weather-plugin/
├── manifest.yaml
├── plugin.wasm
├── schema.json
├── openapi.yaml
├── README.md
```

`manifest.yaml` 示例：

```yaml
id: com.example.weather
name: Weather API
version: 1.0.0
api_version: v1
runtime: wasm
entrypoint: plugin.wasm

permissions:
  network:
    - api.weather.example.com
  secrets:
    - WEATHER_API_KEY

limits:
  timeout_ms: 3000
  memory_mb: 32

hooks:
  - validate_config
  - handle_request
  - health_check
```

### 5. 插件管理功能

- 上传插件
- 校验 Manifest
- 校验文件哈希
- 校验数字签名
- 安装和卸载
- 启用和禁用
- 插件版本管理
- 灰度启用
- 版本回滚
- 插件配置 Schema
- 插件健康检查
- 插件调用统计
- 插件日志查询
- 插件权限声明
- 插件超时和内存限制

默认安全原则：

- 默认不能访问宿主文件系统
- 默认不能读取环境变量
- 默认不能访问任意网络地址
- 只能读取明确授权的 Secret
- 每次执行都有超时
- 每个插件都有内存上限
- 插件输出大小受限制

---

## 六、日志及可观测性规划

### 1. 日志系统

```text
Go slog JSON
    ↓
容器标准输出
    ↓
Grafana Alloy
    ↓
Grafana Loki
    ↓
Grafana
```

同时接入 OpenTelemetry，用于统一产生指标、日志和链路数据。

### 2. 日志分类

#### 访问日志

每个 API 请求记录：

- 时间
- Request ID
- Trace ID
- 环境
- API ID
- 路由 ID
- HTTP 方法
- 请求路径
- 客户端 IP
- 调用者类型
- API Key 前缀
- 用户或租户 ID
- HTTP 状态码
- 总耗时
- 鉴权耗时
- 限流耗时
- 插件耗时
- 上游耗时
- 请求和响应字节数
- 上游地址
- 插件 ID 和版本
- 错误码

#### 审计日志

记录管理端操作：

- 创建、修改、删除 API
- 发布和回滚
- 安装、更新、卸载插件
- 创建、禁用、轮换密钥
- 修改权限和策略
- 用户登录和失败登录
- 修改系统配置

#### 系统日志

- 服务启动和退出
- 配置加载
- 数据库错误
- Redis 错误
- 插件加载失败
- 上游健康检查
- 限流异常
- Panic 恢复
- 后台任务执行结果

### 3. 日志安全

- 默认不记录密码、Token、Cookie、Authorization
- 请求体默认关闭记录
- 按接口开启请求体采样
- 支持字段级脱敏
- 限制单条日志大小
- 审计日志与访问日志分离
- 设置日志保留周期
- Loki 标签只使用环境、应用、路由等低基数字段
- Request ID、Trace ID、用户 ID 等高基数字段放入结构化日志内容

---

## 七、用户系统预留

第一版不一定实现完整用户中心，但数据库和权限模型必须提前预留。

### 预留数据表

```text
users
tenants
tenant_members
roles
permissions
role_permissions
user_roles
service_accounts
api_credentials
login_sessions
audit_logs
```

### 权限模型

采用 RBAC，并保留租户维度：

```text
用户
  → 所属租户
  → 角色
  → 权限
  → 可管理的 API、插件和凭证
```

建议预定义角色：

- Super Admin
- Tenant Admin
- API Developer
- Plugin Developer
- Operator
- Auditor
- Read Only

第一版可以仅实现一个内置管理员，同时让所有业务表提前拥有：

```text
tenant_id
created_by
updated_by
```

---

## 八、数据库与核心实体

建议核心实体如下：

```text
Tenant
User
Role
API
APIVersion
Route
Upstream
Policy
Consumer
Credential
Plugin
PluginVersion
PluginInstance
Release
AuditLog
SystemSetting
```

关键关系：

```text
API
 ├── 多个版本
 ├── 多个路由
 ├── 多个策略
 ├── 一个或多个插件
 └── 一个或多个上游

Consumer
 ├── 多个凭证
 ├── 多个授权范围
 ├── 独立限流策略
 └── 独立调用配额

Plugin
 ├── 多个版本
 ├── 多个实例配置
 └── 绑定多个 API 或路由
```

---

## 九、项目目录规划

```text
api-manager/
├── cmd/
│   ├── server/                 # 主程序
│   ├── apictl/                 # 管理命令行
│   └── plugin-sdk/             # 插件开发辅助工具
├── internal/
│   ├── admin/                  # 管理端 API
│   ├── gateway/                # 数据面
│   ├── router/                 # 动态路由
│   ├── proxy/                  # 上游代理
│   ├── auth/                   # 鉴权
│   ├── ratelimit/              # 限流
│   ├── quota/                  # 配额
│   ├── policy/                 # 策略链
│   ├── plugin/                 # 插件管理和运行时
│   ├── release/                # 发布和回滚
│   ├── observability/          # 日志、指标、追踪
│   ├── audit/                  # 审计日志
│   ├── user/                   # 用户系统预留
│   ├── database/               # 数据访问
│   └── config/                 # 配置加载
├── pkg/
│   └── pluginapi/              # 插件公共协议
├── sdk/
│   ├── go/                     # Go 插件 SDK
│   └── rust/                   # 可选 Rust 插件 SDK
├── plugins/
│   └── examples/               # 示例插件
├── migrations/
├── configs/
├── web/                        # 后续管理界面
├── deploy/
│   ├── docker/
│   └── compose/
├── docs/
├── tests/
│   ├── integration/
│   ├── performance/
│   └── security/
├── Dockerfile
├── compose.yaml
├── Makefile
└── README.md
```

---

## 十、Docker 部署规划

### 1. 开发和单机部署

Docker Compose 包括：

```text
api-manager
postgres
redis
alloy
loki
grafana
prometheus
```

可选组件：

```text
tempo
minio
```

其中 MinIO 可以用来保存插件包及构建产物；小规模环境也可以先使用 Docker Volume。

### 2. 镜像构建

采用多阶段构建：

```text
Go Builder
    ↓
编译静态二进制
    ↓
Distroless 或 Alpine 运行镜像
```

要求：

- 非 root 用户运行
- 只读根文件系统
- `/health/live` 存活检查
- `/health/ready` 就绪检查
- `/metrics` Prometheus 指标
- 配置通过环境变量或挂载文件提供
- Secret 不写入镜像
- 镜像版本与 Git Commit 对应

### 3. 部署模式

支持三种模式：

```text
api-manager serve --mode=all
api-manager serve --mode=control
api-manager serve --mode=gateway
```

小规模部署运行 `all`；后续高并发场景可以分别扩容控制面和数据面。

---

## 十一、开发阶段划分

### 阶段 0：需求确认和技术验证

预计：3～5 个工作日。

工作内容：

- 明确接口规模和预计 QPS
- 明确是否需要管理 Web 页面
- 明确插件主要编写语言
- 明确单租户还是多租户
- 明确日志保留周期
- 编写简化版架构设计
- 验证 WASM 插件调用
- 验证 Redis 分布式限流
- 验证动态路由无中断刷新

产出：

- 需求说明
- 架构图
- 数据模型草案
- 插件 PoC
- 性能基线测试

### 阶段 1：工程基础与部署骨架

预计：1 周。

工作内容：

- 初始化 Go 工程
- 配置管理
- PostgreSQL 和 Redis 接入
- 数据库迁移
- Dockerfile
- Docker Compose
- 健康检查
- Makefile
- CI 流程
- 统一错误码
- JSON 结构化日志
- Request ID 和 Trace ID

产出：

- 可以通过 Docker Compose 启动的空框架
- 基础健康检查和日志链路
- 数据库自动迁移

### 阶段 2：API 管理和核心网关

预计：2 周。

工作内容：

- API CRUD
- 路由 CRUD
- 上游配置
- 动态路由加载
- 反向代理
- 请求超时
- Header 和路径重写
- 配置发布
- 配置版本
- 配置回滚
- OpenAPI 导入基础能力

产出：

- 可以通过管理接口创建和发布代理 API
- 修改配置后不需要重启服务
- 可以回滚到历史版本

### 阶段 3：鉴权、限流和安全策略

预计：2 周。

工作内容：

- API Key
- JWT/JWKS
- HMAC 签名
- Redis 分布式限流
- 日/月调用配额
- IP 黑白名单
- CORS
- 请求大小限制
- JSON Schema 校验
- 凭证创建、禁用和轮换
- 敏感字段脱敏

产出：

- 完整的基础 API 治理能力
- 多实例情况下限流结果一致
- 鉴权和限流具有自动化测试

### 阶段 4：插件系统

预计：2～3 周。

工作内容：

- 插件 Manifest
- 插件包上传
- 插件版本管理
- wazero 运行时
- Handler 插件
- Middleware 插件
- 插件配置校验
- 超时和内存限制
- 插件权限控制
- 插件健康检查
- 插件热更新
- 示例插件
- 插件 SDK
- 插件开发文档

产出：

- 不修改主程序即可增加新的 API
- 插件可以独立升级和回滚
- 至少提供两个示例：Hello World 接口插件、第三方 HTTP API 转换插件

### 阶段 5：日志、指标和审计

预计：1～2 周。

工作内容：

- Loki、Alloy、Grafana 集成
- Prometheus 指标
- API 访问日志
- 管理操作审计
- 插件日志
- 上游请求日志
- 基础 Grafana Dashboard
- 错误率、延迟和限流告警
- 日志采样与脱敏

产出：

- 可查询每次 API 请求
- 可以按 API、路由、插件和状态码统计
- 可以通过 Request ID 定位完整调用过程

### 阶段 6：用户系统预留和管理界面

预计：1～2 周。

工作内容：

- User、Tenant、Role、Permission 数据结构
- 内置管理员
- 管理接口认证
- RBAC 中间件
- 服务账号
- 管理端基础页面
- API、插件、凭证和日志页面

如果首版不需要管理页面，可仅完成管理 API 和 `apictl` 命令行，将工期缩短约一周。

### 阶段 7：测试、安全加固和发布

预计：1～2 周。

工作内容：

- 单元测试
- 集成测试
- 插件兼容性测试
- 并发测试
- 限流正确性测试
- 故障注入
- Redis/PostgreSQL 短时故障测试
- 插件超时和内存溢出测试
- 安全扫描
- 镜像扫描
- 升级和回滚测试
- 运维文档

产出：

- `v0.1.0` MVP
- Docker Compose 部署包
- API 文档
- 插件开发文档
- 运维手册
- 备份恢复手册

---

## 十二、建议开发周期

按照 2 名后端开发、1 名前端或兼职前端估算：

| 版本 | 范围 | 周期 |
|---|---|---:|
| PoC | 动态路由、WASM 插件、基础限流 | 1～2 周 |
| MVP | API 管理、鉴权、限流、插件、Docker、日志 | 6～8 周 |
| 可生产版本 | 用户权限、审计、监控、安全、压力测试 | 10～12 周 |
| 高可用版本 | 控制面/数据面分离、集群配置同步 | 12～16 周 |

如果由单人开发，建议以 10～14 周完成 MVP 作为相对合理的预期。

---

## 十三、MVP 验收标准

第一版建议满足以下验收条件：

1. 可以通过 REST API 创建、修改、发布和下线 API。
2. 配置更新不需要重启网关。
3. 可以安装一个 WASM 插件并由插件提供新接口。
4. 插件可以启用、禁用、升级和回滚。
5. 支持 API Key、JWT 和 HMAC 鉴权。
6. 支持基于 API Key、IP 和路由的分布式限流。
7. 支持每日和每月调用配额。
8. 支持请求大小、超时、CORS、IP 黑白名单。
9. 每次请求都有 Request ID 和结构化访问日志。
10. 可以在 Grafana 中查询访问日志。
11. 可以在 Prometheus/Grafana 中查看 QPS、错误率和延迟。
12. 管理操作写入审计日志。
13. Docker Compose 可以一条命令完成部署。
14. 单个插件异常不会导致整个程序退出。
15. 插件执行具备超时和内存限制。
16. 用户、租户、角色和权限的数据结构已经预留。

---

## 十四、建议的实施优先级

建议第一版不要做成复杂微服务，优先顺序如下：

```text
动态路由
  → 反向代理
  → API 发布和回滚
  → API Key/JWT 鉴权
  → Redis 限流
  → WASM 插件
  → 结构化日志
  → Loki/Grafana
  → 审计日志
  → 用户权限
  → 管理界面
  → 集群和高可用
```

最终推荐方案：

> **Go 单体模块化程序 + PostgreSQL + Redis + WASM 插件 + OpenTelemetry + Loki/Alloy/Grafana + Docker Compose。**

该组合既能保持首版轻量，又为后续数据面水平扩容、独立插件进程、多租户和 Kubernetes 部署保留演进空间。
