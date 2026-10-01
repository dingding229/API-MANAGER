# API Manager

API 网关与管理控制台，支持接口发布、API Key 管理、限流与配额、上游代理、WASM 插件及运行观测。

## 一键部署

需要 Docker 和 Docker Compose。项目仅提供一个 `docker-compose.yml`，同时启动：

- API Manager：`docker.io/dingding229/api-manager:latest`
- PostgreSQL 17：保存接口、调用凭据和审计数据
- Redis 7：分布式限流与配额

```bash
# 私有 Docker Hub 仓库需要先登录
docker login

# 首次部署生成凭据，只需执行一次
python3 scripts/init-production-secrets.py

# 启动全部服务
docker compose up -d
```

初始化脚本生成 `secrets/` 目录及五个相互独立的凭据，不会显示或覆盖已有值；升级时只补齐缺失文件。应用使用只读文件挂载读取凭据，不需要手工填写数据库地址、DSN 或加密密钥。

访问 `http://127.0.0.1:8080/admin/`，使用用户名和密码登录。

- **首次部署**：默认用户名 `admin`，初始密码在 `secrets/admin_password`；可通过 `ADMIN_USERNAME` 设置首次创建的用户名。
- **已有部署**：继续使用原用户名（或邮箱）及密码。自动初始化不会覆盖已有也不会重置密码。
- 管理控制台使用服务端会话，默认有效期 12 小时；退出登录会使会话失效。业务 KEY、指标 KEY 都不能代替账号登录。

```bash
cat secrets/admin_password
```

请仅在可信终端读取密码，不要把密码放入 URL、聊天记录或源代码。

## API 验证方式

业务 API 仅提供两种方式：

| 方式 | 说明 |
| --- | --- |
| **KEY**（默认） | 必须携带有效、未过期且未吊销的调用 KEY |
| **无需验证** | 显式配置为 `none`，允许匿名调用；仍执行限流及参数校验 |

KEY 请求示例：

```bash
curl -H 'X-API-Key: <调用 KEY>' http://127.0.0.1:8080/api/example
```

也支持 `Authorization: Bearer <KEY>` 作为业务 KEY 的传输格式。不要同时提供两个认证头。调用 KEY 在控制台“调用凭证”中创建、轮换和吊销。

无需验证的接口应仅用于公开数据。将接口的“鉴权”设置为“无需验证”后保存、发布，即可不带 KEY 调用。

控制台用户名密码会话、业务调用 KEY 和独立指标 KEY 相互隔离。`secrets/metrics_token` 仅用于 `/metrics` 抓取，不能登录控制台或访问管理 API。

客户端认证头不会转发到上游。需要调用上游 KEY 时，使用服务端托管且绑定目标 Origin 的 `upstream_auth_ref`，不要把上游秘密写入接口定义。

## 更新

```bash
docker compose pull api-manager
docker compose up -d --force-recreate api-manager
```

默认使用 Docker Hub 的 `latest`，无需修改镜像版本或摘要。更新不会重新生成凭据或删除数据卷。也可以执行 `make update`；该命令会先检查本地凭据和 Compose 配置。

## 配置

不需要 `.env` 即可启动。需要修改端口或资源时，可参照 `.env.example` 设置以下可选项：

| 配置 | 默认值 | 说明 |
| --- | --- | --- |
| `ADMIN_USERNAME` | `admin` | 仅用于首次自动创建管理员，不会修改已有用户 |
| `API_MANAGER_IMAGE` | `docker.io/dingding229/api-manager:latest` | Docker Hub 镜像标签 |
| `SECRETS_DIR` | `./secrets` | 只读凭据文件目录 |
| `API_BIND_ADDR` | `127.0.0.1` | 统一入口的宿主机绑定地址 |
| `API_PORT` | `8080` | 前台、管理后台和业务 API 的统一端口 |
| `ADMIN_PATH` | `/admin` | 管理后台路径，可自定义 |
| `OBSERVABILITY_STACK_ENABLED` | `false` | 启动完整观测栈 |
| `API_MEMORY_LIMIT` | `2g` | API Manager 容器内存上限 |
| `API_CPUS` | `2.0` | API Manager CPU 上限 |

修改配置后执行 `docker compose up -d`。

### 观测

API Manager 内置日志、指标、Trace、告警和 Dashboard。镜像还包含 Loki、Alloy、Tempo、Prometheus、Alertmanager。

启用五组件观测栈：

```bash
OBSERVABILITY_STACK_ENABLED=true docker compose up -d api-manager
```


### 检查状态

```bash
docker compose ps
docker compose logs -f api-manager
python3 scripts/preflight-production.py
python3 scripts/verify-production.py --url http://127.0.0.1:8080
```

- `/health/live`：进程存活
- `/health/ready`：PostgreSQL、Redis 就绪
- `/metrics`：使用独立指标 KEY

## 数据与备份

PostgreSQL、Redis、插件、插件库和观测数据使用独立的持久卷。

- `docker compose down` 保留数据。
- **不要执行 `docker compose down -v`，它会删除数据卷。**
- `secrets/credential_encryption_key` 必须长期保存，更换或丢失会导致已有调用凭据无法解密。
- `secrets/` 必须单独加密备份。目录权限为 `0700`，文件为 `0444`，以支持非 root 容器的只读文件挂载。

在停止 API 和 Redis 写入后执行备份，再恢复服务：

```bash
docker compose stop api-manager redis
python3 scripts/backup-production.py --output-dir /absolute/path/backups
docker compose up -d redis api-manager
```

备份包含 PostgreSQL 逻辑转储、Redis 数据卷、插件、插件库和观测数据。定期在隔离环境演练恢复。

## 安全与升级注意事项

- 数据库和 Redis 不向宿主机开放端口，仅连接 Compose 的内部网络，并启用独立密码认证。
- 内部数据库连接通过显式配置允许私有 Docker 网络内的明文链路；这不等于端到端加密。需要抵御宿主机或网络管理员读取流量时，应使用外部 TLS 数据库。
- 外部 PostgreSQL 必须使用 `sslmode=verify-full`，外部 Redis 和上游 API 必须使用 TLS。TLS 使用系统信任根，不提供私有 CA 注入或跳过证书验证。
- 公网访问必须使用 HTTPS 反向代理，不要直接暴露明文管理端口。管理账号和密码不应交给业务调用方。
- 控制台会话只保存在当前浏览器会话中；退出会清除缓存并使服务端会话失效。用户被禁用后，其已有会话立即失效。
- 更新旧部署前先备份，保留原账号密码、凭据加密密钥及 PostgreSQL 密码。外部数据库中的数据需要先迁移到新 PostgreSQL，不能仅启动一个空数据库替代。
- 已有 KEY 接口不会自动降级为公开接口；需要公开时请在控制台显式改为“无需验证”。旧插件认证声明只允许 `api_key` 或 `none`。
- 单实例及同容器观测栈不提供组件级隔离或高可用。上线前完成容量测试和备份恢复验证。

## Kubernetes

`deploy/helm/api-manager` 为可选 Helm Chart，同样使用 Docker Hub `latest` 、用户名密码登录及业务 API KEY/无需验证。Kubernetes 模式需要现有 PostgreSQL、Redis、Secret、持久卷及明确的 NetworkPolicy 放行规则；外部数据库默认要求 TLS。常规部署优先使用上面的单文件 Compose。

## 统一入口与公开接口文档

主程序镜像包含管理后台、Fumadocs 公开文档及观测组件。Compose 只运行 `api-manager`、`postgres`、`redis` 三个服务，主程序只发布一个 HTTP 端口。

| 功能 | 默认路径 |
| --- | --- |
| 公开接口文档 | `/` |
| 公开目录数据（只读） | `/catalog.json` |
| 管理后台 | `/admin/` |
| 业务 API | `/api/` |

例如将 `API_PORT=8081`、`API_BIND_ADDR=0.0.0.0` 写入服务器 `.env` 后，前台和后台分别访问 `http://服务器IP:8081/` 和 `http://服务器IP:8081/admin/`，前后台共用同一个入口。

### 自定义管理路径

```env
ADMIN_PATH=/operations
```

执行 `docker compose up -d --force-recreate api-manager` 后，后台路径为 `/operations/`。`ADMIN_PATH` 必须是非保留的绝对路径，不可包含查询字符串、路径穿越或结尾斜杠；自定义路径不是认证措施。管理 API 保持 `/admin/v1/`，仍强制用户名密码会话和角色权限。

### 运行观测

后台“运行观测”由主程序提供指标、日志、链路、基础告警与 Dashboard。开启 `OBSERVABILITY_STACK_ENABLED=true` 可同时运行 Loki、Alloy、Tempo、Prometheus、Alertmanager 五个组件，增强日志、链路和指标的采集与持久化；不启用时，自研观测页面仍然可用。

完整栈建议宿主机至少提供 4 GiB 内存，主程序容器配置 `API_MEMORY_LIMIT=2g` 或更高。所有观测组件仅监听容器回环地址，不增加宿主机公开端口。正式公网登录应使用 HTTPS。

### 发布公开接口

在后台编辑接口时填写独立的公开标题、分类和说明，并开启公开展示。接口必须已启用、已发布、明确开启公开开关，且认证方式为 KEY 或“无需验证”。默认隐藏；关闭开关后目录输出立即移除。

目录只包含公开标题、说明、分类、方法、路径、认证方式和参数名称/位置/类型/必填标记，不输出上游地址、凭据、私有说明、真实响应或管理数据。前台不读取后台浏览器会话，也不提供 KEY 输入或在线调用功能。

`PUBLIC_API_BASE_URL` 设置业务调用示例使用的独立域名；不设置时使用占位域名。公开页面是构建时写入镜像的静态文件，数据仅来自只读目录投影；更新主程序镜像会同时更新前后台。

## 用户与权限扩展

后台登录使用独立的用户名和密码。邮箱是独立的联系字段，用于密码找回；旧部署的登录名保持不变。密码要求至少 8 个 UTF-8 字节，且仅保存 bcrypt 哈希。登录返回服务端随机会话，数据库只保存会话哈希；会话不是 JWT 或业务 KEY。退出、过期和禁用用户会使会话失效，角色权限在每次管理请求时重新检查。

已保留用户、角色、权限、用户角色关系和会话表，以及用户创建、用户名/邮箱与密码修改、状态修改、角色分配和自定义角色接口。修改自己的基本信息需要当前密码确认；修改用户名、邮箱或密码会撤销该用户已有会话。当前采用共享 API 资源的 RBAC；这不等于租户级数据隔离。后续增加独立租户时，应同时实现资源归属与查询过滤。

旧版本遗留的 `admin_token` 文件不再用于认证或挂载；升级时不删除已有凭据文件，也不重置已有账号密码。

### 邮箱和密码找回

“用户管理 → 编辑信息”或“账号设置”可绑定独立邮箱。邮箱不能替代用户名登录。历史账号原有登录名、密码、角色和会话保留；原登录名本身是邮箱地址的账号，会将该地址初始化为联系邮箱。

登录页提供“忘记密码”，向绑定邮箱发送一次性链接。链接有效期 15 分钟，成功使用后立即作废，并撤销账号全部旧会话。修改用户名、邮箱或密码也会使旧重置链接失效。未绑定邮箱的账号须由管理员重置。

邮件发送使用 SMTP TLS。首次运行新版 `scripts/init-production-secrets.py` 会新增空的 `secrets/smtp_password`，不会覆盖原凭据。默认不启用邮件；未配置时界面会明确提示管理员尚未配置，绝不会把重置令牌返回到页面。

在 `.env` 中设置 `SMTP_HOST`、`SMTP_PORT`（默认 587）、`SMTP_MODE`（`starttls` 或 `tls`）、`SMTP_FROM`、`SMTP_USERNAME` 和 `PASSWORD_RESET_BASE_URL`（管理后台的完整 HTTPS 地址，例如 `https://manager.example.com/admin/`）。将 SMTP 密码写入 `secrets/smtp_password`，恢复该文件权限为 `0444`，再重建主程序。不要把 SMTP 密码写入 Git 或聊天记录。

### 总览统计

总览显示接口、已发布接口、有效凭证、启用插件、用户数量及服务状态；拥有 `observability.read` 的账号还能查看业务请求总数、近 5 分钟请求数、成功率、平均/P95 响应时间、失败请求和最近一小时趋势。业务计数不包含后台、静态资源和健康检查；HTTP 总数单独显示。统计范围为当前主程序进程，重启后累计值重新统计，不是历史账单统计。

## 镜像构建

`Dockerfile` 从不可变上游源码构建全部观测组件。`Dockerfile.release` 仅从已审核且摘要固定的发行镜像提取五个观测组件执行文件，最终镜像使用独立的干净系统基底，并重建主程序与静态前端；不会继承提取来源的其他程序、静态资源、镜像层或端口配置。发布前仍需扫描最终镜像。部署的运行镜像保持 Docker Hub `latest`，构建基底固定不影响部署更新。
