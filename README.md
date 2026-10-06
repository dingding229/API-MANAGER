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

初始化脚本生成 `secrets/` 目录及五个相互独立的凭据、可选 SMTP 密码和版本检查令牌文件，不会显示或覆盖已有值；升级时只补齐缺失文件。应用使用只读文件挂载读取凭据，不需要手工填写数据库地址、DSN 或加密密钥。

通过同一个 **HTTPS 网站地址**访问 `/admin/` 管理后台与 `/account` 用户中心，使用用户名和密码登录。生产会话 Cookie 强制启用 Secure，公网 HTTP/IP 地址不适合登录。服务默认只监听宿主机回环端口，由可信反向代理提供 HTTPS。

- **首次部署**：使用 `secrets/admin_bootstrap_key` 中的一次性密钥进入管理员注册页，自行设置用户名、独立邮箱和密码；不再生成管理员密码。注册成功后密钥永久失效，重启或删除账号都不会重新开放注册。
- **升级部署**：保留已有账号、密码、角色和会话；一次性注册入口自动关闭。
- 管理控制台使用服务端会话，默认有效期 12 小时；退出登录会使会话失效。业务 KEY、指标 KEY 都不能代替账号登录。

```bash
python3 - <<'PY'
from pathlib import Path
key = Path("secrets/admin_bootstrap_key").read_text().strip()
print("http://127.0.0.1:8080/admin/#setup=" + key)
PY
```

请仅在可信终端读取一次性密钥，不要将密码或完整注册链接分享、提交到代码仓库或写入日志。

## 用户中心与计费

- `/account` 提供账户概览、昵称和邮箱资料、余额与流水、套餐、自己的调用凭据、调用日志、设备与会话管理。
- UID 自动生成且不可修改。用户名与邮箱独立。密码长度为 8–72 个 UTF-8 字节；修改用户名、邮箱或密码会撤销所有旧会话。
- 会员等级沿用角色权限体系，展示名称和权限名称可以自定义。默认注册等级为 `member`，只有公开在线测试权限，不授予后台管理权限。
- 管理员在“会员与计费”配置每小时、每日、每月额度的套餐，并核实后调整余额；没有接入线上支付。调整金额须填写原因并重新验证管理员密码与双重验证，保留审计流水。
- 金额以整数微元保存，1 元 = 1,000,000 微元，最多 6 位小数。接口默认免费；收费接口必须使用 KEY，且凭据绑定用户。旧的未绑定 KEY 仍可调用免费接口，不能调用收费接口。
- 每次调用先预留余额或套餐额度，2xx 响应完成扣费，其他响应释放预留余额和套餐额度。插件缓存命中也属于一次调用。有效套餐覆盖调用费用；额度耗尽后拒绝调用，不自动改为余额扣费。
- 套餐额度按北京时间自然小时、自然日、自然月统计。0 表示该周期不限，至少一项额度限制须大于 0。购买时快照保存额度；管理员修改套餐不影响已购买订单。允许一份待生效续购，当前套餐到期后开始。
- 超时重试购买和余额调整必须复用原操作编号，不可重复创建订单。请求结果不确定的扣费保留预留金额，10 分钟后进入“待核对的调用结算”，由管理员核对实际结果后结算，不自动退款。
- 用户只可查看自己的最近 100 条调用记录和余额流水。调用日志默认保留 90 天，分批清理；财务流水不会自动删除。调用记录不保存密钥、请求参数或响应正文；财务流水与审计日志应按业务要求保留和备份。

## 注册、验证与授权登录

在管理后台“注册与登录”配置开放注册、邮箱验证码、GitHub/Google 授权登录与 Cloudflare Turnstile。新部署默认关闭这些功能。用户系统、邮箱验证码和财务功能依赖 PostgreSQL；内存模式仅适用于旧功能的非生产调试。

1. **网站与邮件**：先配置实际 HTTPS 网站地址，以及“网站设置”中的 SMTP。密码找回使用邮件发送的 15 分钟一次性链接；新邮箱变更需要验证新邮箱。邮箱验证码 10 分钟有效，最多 5 次尝试，同一邮箱每小时最多发送 3 次。
2. **TOTP 双重验证**：用户在“账号安全”通过当前密码绑定兼容 TOTP 的验证器，使用 SHA1、6 位数字、30 秒周期；绑定信息 10 分钟有效。绑定密钥加密保存，动态码时间步和恢复码不可重放。启用和关闭均撤销全部旧会话。恢复码仅显示一次，请离线保存。密码、邮箱验证码、第三方授权登录以及密码重置都必须通过已启用的双重验证；丢失验证器时使用恢复码。
3. **第三方授权**：使用授权码与 PKCE，通过服务端读取提供者资料，身份由提供者和唯一 subject 绑定；不按邮箱自动合并账号。已有用户登录后可重新验证并绑定或解绑授权账号。首次第三方注册会生成随机本地密码，用户可通过邮箱找回流程设置自己知道的密码，再配置 TOTP。
4. **Turnstile**：配置站点 Key、Secret 和主机名；服务端校验成功结果、主机名与具体操作，未通过时拒绝关键操作。验证码只使用一次，过期须重新验证。启用前请确认站点 Key 可在真实域名正常工作，否则会阻止登录；保留数据库备份与安全的服务器恢复途径。
5. **会话与反向代理**：前后台共用一个 host-only HttpOnly、Secure、SameSite Cookie。不要混用 IP、不同域名或子域名。仅把实际可信入口地址加入 `TRUSTED_PROXY_CIDRS`，不得信任所有来源；否则登录 IP 与防滥用限制不可靠。

第三方回调地址（将域名替换为实际网站域名）：

```text
https://实际网站域名/account/v1/oauth/github/callback
https://实际网站域名/account/v1/oauth/google/callback
```

SMTP 密码、OAuth Secret、Turnstile Secret、TOTP 绑定密钥与插件缓存使用主程序加密密钥保护；升级与备份必须保留原加密密钥。没有提供者的有效凭据时，不要开启对应功能。

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
| `ADMIN_BOOTSTRAP_KEY_FILE` | `/run/secrets/admin_bootstrap_key` | 首个管理员的一次性注册密钥；已有账号时不可使用 |
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
- 前后台通过同一主机的 HttpOnly Cookie 共享登录会话；退出会清除登录 Cookie 并使服务端会话失效。用户被禁用后，其已有会话立即失效。
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

在“运行观测 → 应用日志”中可清理全部应用日志。该操作需要 `observability.logs.clear` 权限，默认仅超级管理员拥有，可由超级管理员在角色权限中单独授予；勾选确认后才可执行。清理范围包含内存索引和已保存的应用日志文件，不包含审计日志、请求链路、统计数据、Docker 日志或外部观测系统的数据。清理请求与结果记入审计日志；无法保存审计信息时不会执行清理。服务会继续记录后续日志。

后台“运行观测”由主程序提供指标、日志、链路、基础告警与 Dashboard。开启 `OBSERVABILITY_STACK_ENABLED=true` 可同时运行 Loki、Alloy、Tempo、Prometheus、Alertmanager 五个组件，增强日志、链路和指标的采集与持久化；不启用时，自研观测页面仍然可用。

完整栈建议宿主机至少提供 4 GiB 内存，主程序容器配置 `API_MEMORY_LIMIT=2g` 或更高。所有观测组件仅监听容器回环地址，不增加宿主机公开端口。正式公网登录应使用 HTTPS。

### 发布公开接口

在后台编辑接口时填写独立的公开标题、分类和说明，并开启公开展示。接口必须已启用、已发布、明确开启公开开关，且认证方式为 KEY 或“无需验证”。默认隐藏；关闭开关后目录输出立即移除。

目录只包含公开标题、说明、分类、方法、路径、认证方式和参数名称/位置/类型/必填标记，不输出上游地址、凭据、私有说明、真实响应或管理数据。前台不读取后台浏览器会话，也不提供 KEY 输入或在线调用功能。

调用地址通过后台的网站地址和接口专用域名设置管理。未设置时使用当前网站访问地址。更新主程序镜像会同时更新前后台。

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


### 管理员注册与接口方法

一次性注册链接中的密钥放在 URL 片段 `#setup=`，不会发送到 HTTP 访问日志；页面读取后立即清除地址栏片段。请将示例链接的地址和 `/admin/` 路径替换为实际 HTTPS 地址和自定义后台路径，不要分享注册链接。首次注册必填独立邮箱；升级不会覆盖旧账号。注册完成后使用用户名和密码登录，密码长度为 **8–72 个 UTF-8 字节**。

每个接口可选择一种或多种请求方法，所选方法共用鉴权、配额、插件/上游和发布状态。鉴权下拉仅包含 **KEY** 和 **无需验证**。旧版单方法接口会自动迁移；OpenAPI 导出保留每种方法；公开目录将同一路径合并为一个接口，在详情中切换调用方式。

生产环境必须通过可信 HTTPS 入口访问。Compose 默认只监听回环地址，示例中的 HTTP 仅用于本机连通性检查；如果直接暴露公网 HTTP 端口，密码、会话和调用密钥仍存在被窃听风险，不能视为完整的生产安全配置。SMTP 未配置时找回密码不可用，请配置邮件服务并实际验证送达。

更新前先备份数据库、Secrets 和数据卷。不要运行全局 `docker system prune --volumes`；测试清理应只针对单独测试项目及其明确命名的卷，保留生产账号、接口、凭据、审计记录和观测数据。


## 超级管理员网站设置

超级管理员登录后可进入 **网站设置**，配置网站名称、公开页面与后台浏览器 title、SEO 描述和关键词、网站地址、接口专用域名、首页标题与介绍、公告、页脚及公开联系邮箱。展示内容以纯文本处理，公开页面只读取经过白名单投影的网站资料，不能读取管理接口或 SMTP 配置。

邮件配置支持 SMTP 主机、端口、STARTTLS/隐式 TLS、用户名、密码或授权码、发件邮箱及密码重置页面地址。先保存设置，再发送测试邮件确认服务器接收；是否到达收件箱仍需实际检查。启用 SMTP 时必须配置可信 HTTPS 后台重置地址，证书验证不能关闭。为保护内部服务，通过后台配置的 SMTP 不能访问私网、回环及云元数据地址。

网站设置保存在 PostgreSQL，保存后立即生效，容器重启后仍保留。SMTP 密码加密存储，不在任何读取接口回显；密码框留空保留原值，只有显式勾选清除才会删除。只有 `super_admin` 可以查看或修改邮件设置、发送测试邮件，其他管理员不能通过修改角色权限获得此能力。配置变更采用版本检查并记入审计日志，审计记录不包含密码。

尚未通过后台保存时沿用环境变量中的 SMTP 与公开 API 地址；首次保存后以数据库中的网站设置为准。更换 `credential_encryption_key` 会影响已加密的 SMTP 密码，请保留密钥与数据库配套备份。监听端口、数据库/Redis 凭据、后台路由和数据目录属于部署边界，仍通过服务器配置管理，不从网页热修改。


## 接口专用域名

超级管理员可在 **网站设置 → 接口专用域名** 填写独立域名或完整地址，例如 `api.example.com` 或 `https://api.example.com`。需要使用非默认端口时填写完整地址，例如 `http://api.example.com:8081`。专用域名不能与网站域名相同。

留空时，接口仍可通过网站地址调用，示例优先使用已设置的网站地址，未设置时使用浏览器当前访问地址。填写专用域名后，`/api/` 下的业务调用只接受该地址，网站地址和伪造的转发域名头会被拒绝；首页、后台、登录及健康检查不受影响。清空设置后恢复原有调用方式。设置保存后立即生效，重启后保留。

域名解析、证书和反向代理仍需配置到本服务。代理必须保留原始 `Host`；应用不会信任客户端提供的 `X-Forwarded-Host` 或 `Forwarded`。域名限制不能代替 KEY 认证。如果 HTTPS 在代理处终止，代理也应保证 TLS 域名与请求域名一致，生产业务不要直接暴露未加密的管理端口。

公开目录中，每个接口只显示一次；不同请求方式的认证和参数分别保留。调用示例自动显示实际调用地址，参数使用可编辑的示例值，不公开真实密钥或后台配置中的敏感默认值。

## 文档页在线测试

公开文档仍可匿名浏览。已有用户可点击“登录测试”，使用用户名和密码登录；同一主机上的前台与管理后台共享登录会话，任一入口登录后另一入口无需重复登录。在线测试仅使用已公开并发布的接口，发送前会重新读取目录；下线、隐藏或变更调用域名后，需要刷新文档。

参数编辑只更新示例，不会自动请求。测试时须勾选真实调用确认并点击“发送请求”；请求会消耗正常接口额度，写入类操作可能修改业务数据。停止等待或网络超时**不会撤销已经执行的请求**，不要盲目重试。

测试通过本站的受限测试入口执行，不下发真实调用 KEY，也不接受任意目标 URL。登录用户先取得与会话和请求绑定的一次性授权，仅可测试管理员明确开放的接口。业务接口仍使用 KEY/无需验证，不接受网站登录 Cookie 代替 KEY。页面不将测试授权或响应写入浏览器存储；切换接口、请求方式或退出登录会清除页面响应。

配置专用 API 域名时，须同时在“网站设置”中填写网站的完整 HTTPS 地址。程序仅为该网站来源开放业务接口的跨域读取，不开放跨域管理/登录接口，不携带登录 Cookie。公开页使用本站测试入口，调用地址和域名限制由服务端检查。GET/HEAD 声明了请求内容时须使用外部调用工具；响应最多展示 128 KiB，等待最多 15 秒。

## 程序版本与更新检查

管理后台侧栏显示当前程序版本，登录后自动检查，也可点击“检查更新”。正式发布镜像在构建时写入版本标签和源码提交号；本地构建显示 `-dev`，不将其误报为正式最新版本。检查以本项目 GitHub 仓库的稳定版本标签为依据，不计预发布标签，也不等同于 Docker Hub 镜像已经完成发布。检查成功的结果缓存一小时，失败缓存五分钟；检查不会自动更新镜像或修改数据。

本仓库为私有仓库。没有只读凭据时，后台会提示无法检查，而不是显示“已是最新”。运行更新版 `python3 scripts/init-production-secrets.py` 可补齐空的 `secrets/github_update_token`，不会覆盖任何原凭据。需要启用检查时，在 GitHub 创建仅限 `dingding229/API-MANAGER`、仅 `Contents: Read-only` 的细粒度令牌，写入此文件，恢复 `0444` 文件权限（父目录 `0700`），重建主程序。令牌只用于固定的 GitHub 查询，不在网页、日志或公开目录中回显。非 Compose 部署可使用 `GITHUB_UPDATE_TOKEN_FILE`；无需写入网站设置或源码。

升级继续使用 Docker Hub 的 `latest` 镜像：

```bash
python3 scripts/init-production-secrets.py
python3 scripts/preflight-production.py
docker compose pull api-manager
docker compose up -d --no-deps api-manager
```

## Cloudflare CDN 与缓存

生产入口使用 HTTPS 域名，建议源站配置受信任的公有 CA 证书并采用 **Full (strict)**，不要使用 Flexible。Cloudflare 常规代理不支持访客 URL 使用 `8081` 端口；应由源站反向代理在 `443` 接收请求，再转发到程序监听端口，保留原始 `Host`。数据库和 Redis 不公开，源站程序端口限制为回环或可信入口访问。

| 内容 | 缓存策略 | 原因 |
| --- | --- | --- |
| `/_next/static/` 下成功返回、无凭据的构建指纹 JS/CSS/字体等 | 可长期缓存，`public, max-age=31536000, immutable` | 内容随构建路径变化，多个访客共享相同资源 |
| `/` 公开 HTML | 不缓存 | 标题、SEO 和网站信息可在后台即时调整 |
| `/catalog.json`、`/public/v1/*` | 不缓存 | 目录可即时隐藏、下线；缓存可能继续展示过期信息 |
| `/test/v1/*`、`/auth/*` | 不缓存 | 登录状态、Cookie 和账号信息 |
| 管理页面及资源（默认 `/admin`；自定义路径也相同）、`/admin/v1/*` | 不缓存 | 权限、设置、版本状态及未带指纹的管理资源 |
| `/api/*`，包括无需验证的接口 | 不缓存 | 业务数据、额度、可能个性化的响应；不能按扩展名判断 |
| `/health/*`、`/metrics` 和错误响应 | 不缓存 | 当前服务状态或受保护信息 |

程序在最终响应边界统一限制动态内容，避免上游的 `Cache-Control`/`Expires` 意外使业务响应被 CDN 缓存。动态响应发送 `Cache-Control: private, no-store`、`CDN-Cache-Control: no-store` 和 `Cloudflare-CDN-Cache-Control: no-store`。仅符合静态白名单且未设置 Cookie 的成功资源发送长期缓存头。

### 推荐 Cache Rules

不要为整个网站开启 Cache Everything，也不要使用忽略源站缓存头的 Edge TTL。推荐两条规则（将 `docs.example.com` 替换为实际网站域名）：

1. **公开静态资源**：仅网站域名的 `/_next/static/`，GET/HEAD，无 Cookie、Authorization、X-API-Key。设置 Eligible for cache，Edge TTL 选择“使用源站缓存控制；不存在时绕过缓存”，Browser TTL 尊重源站。
2. **其他请求全部绕过**：使用下方表达式，设置 Bypass cache，放在其他缓存规则之后，避免被更宽泛规则覆盖。API 专用域名也由这条规则绕过。

静态资源匹配表达式：

```text
(http.host eq "docs.example.com"
 and starts_with(http.request.uri.path, "/_next/static/")
 and http.request.method in {"GET" "HEAD"}
 and not any(http.request.headers.names[*] eq "authorization")
 and not any(http.request.headers.names[*] eq "x-api-key")
 and not any(http.request.headers.names[*] eq "cookie"))
```

“其他请求全部绕过”使用上述整个表达式的否定，即 `not (...)`。这比逐一罗列后台路径更安全，修改 `/admin` 路径后不需要补充例外。规则应适用于网站和 API 域名，不设置忽略查询参数或忽略 Host 的共享缓存键。若已使用 Workers、Page Rules 或其他缓存规则，须检查它们未覆写上述保护。

上线后使用实际 HTTPS 域名验证：

```bash
# 目录、登录、后台、业务 API 均不应出现 CF-Cache-Status: HIT。
curl -sSI https://docs.example.com/catalog.json
curl -sSI https://docs.example.com/admin/
# 从网页源码取真实带构建指纹的资源 URL，连续请求检查缓存。
curl -sSI 'https://docs.example.com/_next/static/chunks/实际文件名.js'
```

HEAD 仅用于检查响应头；部分接口不支持 HEAD。不要为检查缓存而请求可能有业务副作用的接口。上线切换 CDN 前清除旧缓存，再验证后台变更、下线接口不会被旧目录继续展示。`CF-Cache-Status` 为 HIT 才说明边缘命中；BYPASS/DYNAMIC 是动态路由的正常结果。

官方参考：[默认缓存行为](https://developers.cloudflare.com/cache/concepts/default-cache-behavior/)、[Cache Rules 设置](https://developers.cloudflare.com/cache/how-to/cache-rules/settings/)、[规则顺序](https://developers.cloudflare.com/cache/how-to/cache-rules/order/)、[网络端口](https://developers.cloudflare.com/fundamentals/reference/network-ports/)、[Full (strict)](https://developers.cloudflare.com/ssl/origin-configuration/ssl-modes/full-strict/)。

## 插件结果数据库缓存

在“接口管理”创建或编辑插件接口时，展开“插件数据缓存”，可配置：

- **启用缓存**：默认关闭，现有接口不会自动改变行为。
- **有效期**：1～604800 秒（最长 7 天），界面默认 300 秒；过期后执行插件并重新写入结果。
- **最多缓存条目**：1～10000 条，界面默认 1000 条；超过上限淘汰较早写入的条目。
- **缓存 POST 查询**：默认关闭，只有确认 POST 是只读查询且重复执行没有业务副作用时才启用。

缓存由主程序管理，存储在 PostgreSQL 的 `plugin_response_cache` 表中；已有插件无需修改，也无需开放 `PLUGIN_DATABASE_WRITES_ENABLED` 或向 WASM 暴露数据库权限。缓存内容使用主程序凭据加密密钥加密，必须保留原加密密钥。容器重启后未过期的缓存仍可使用。

仅缓存 HTTP 200、未超出 1 MiB、通过响应校验的结果；错误响应、流式响应、设置业务 Cookie、明确要求不缓存的响应不写入缓存。GET/HEAD 默认可缓存，PUT/PATCH/DELETE 不缓存。携带业务 Cookie、Range、条件请求或明确要求刷新/不缓存的请求会执行插件，不使用旧缓存。

缓存键区分接口、插件版本、调用方式、路径、原始查询参数、请求内容、调用凭据及其他业务请求头；跟踪编号等关联标识不作为业务缓存键。接口配置或插件升级后旧缓存不再命中。命中缓存仍须通过鉴权、参数检查、限流和配额，已吊销的 KEY 不能读取缓存。

编辑已有插件接口时可点击“查看与清理缓存”，读取有效/过期条目数量；具有接口修改权限的用户在确认后可清理该接口缓存。清理前记录审计，并防止已经执行中的旧请求重新写入刚清理的数据。过期数据由后台定期分批清理。数据库缓存临时不可用时退回正常插件执行，不影响接口鉴权。

可通过响应头 `X-Plugin-Cache: HIT` 或 `MISS` 查看命中情况。此功能是服务端数据库缓存，与浏览器/Cloudflare CDN 缓存无关：业务接口对外仍发送 `no-store`。在线测试的浏览器请求要求不使用旧缓存时，会按此要求执行插件。

## 前后台统一登录

前台与后台共享同一主机下的一个 HttpOnly、SameSite=Strict 登录 Cookie。任一入口登录后，另一入口不需要再次输入密码；任一入口退出、用户被禁用，或者修改账号信息导致会话失效后，两个入口均不能继续使用原会话。页面切换、窗口重新获得焦点和登录事件会同步状态；长时间停留也会定期重新检查。账号密码、角色权限和会话有效期仍沿用原用户系统。

生产环境的登录 Cookie 强制 Secure，须使用 HTTPS 域名。前台和后台必须使用同一个主机地址，不要一边使用 IP、一边使用域名，或使用不同子域名，否则浏览器不能共享主机限定的 Cookie。浏览器无法保存会话时页面会明确提示，而不会仅凭密码验证成功就显示已登录。

升级时会将有效的旧后台浏览器会话或旧前台测试 Cookie 转换为统一会话。浏览器不再将新的登录令牌保存在 sessionStorage/localStorage；旧令牌迁移后立即移除。使用用户名密码登录的程序化 Bearer 会话仍兼容，业务 API 的 KEY/无需验证方式没有改变。

管理接口使用 Cookie 执行修改操作时必须携带本站 `Origin` 和 `X-API-Request: 1`，防止跨站伪造操作。公开目录仍只读取公开数据，前台不调用管理 API；登录 Cookie 不用于业务接口鉴权，也不会传给 WASM 插件或上游服务。

缓存统计和清理接口分别为 `GET /admin/v1/apis/{id}/cache` 与 `DELETE /admin/v1/apis/{id}/cache`；清理请求内容为 `{"confirm":true}`，分别要求接口查看和接口修改权限。响应只返回配置、数量或清理结果，不返回缓存内容或请求密钥。浏览器调用同样需要统一会话和本站请求保护头。

## 插件开发与登录管理

插件包、ABI、运行边界、响应格式、缓存规则及交付要求参见 [插件开发规范](PLUGIN_DEVELOPMENT.md)。后台新增“登录会话”，可查看登录时间、最近访问、来源 IP 和设备信息，并在确认后退出指定会话。普通用户仅管理自己的会话，其他用户的会话需 `user.sessions.manage` 权限；超级管理员会话不能由普通管理员管理。退出立即使该会话的未使用测试授权失效。

来源 IP 默认记录实际连接地址，不相信外部请求自行填写的转发头。使用可信反向代理时，在 `TRUSTED_PROXY_CIDRS` 中仅填写可信入口网段，并让入口正确追加 `X-Forwarded-For`；应用从右向左验证代理链，无法验证时保留实际连接地址。不要填写 `0.0.0.0/0` 或直接信任任意来源。设备名称由 User-Agent 推断，不能作为不可伪造的设备证明。

公开页面的在线测试默认关闭，需在接口管理中单独开启。登录用户无需填写 KEY，但每次须先获得 30 秒内有效、与会话、请求、接口版本、IP 和客户端信息绑定的单次授权；修改参数、重放、会话失效或未开放的接口都会拒绝。每账号最多每分钟 10 次、每日 200 次执行，最多保留 3 份待用授权。只读账号限 GET/HEAD，其他方式需接口修改权限。授权不能用于直接访问 `/api/*`，真实调用 KEY 不发送到网页。

Cookie 加单次授权不能保证合法用户无法自动化复现整个流程；请继续限制账号权限、测试额度和入口访问。HTTP 来源、跨站请求、已退出会话或来源变化会受到限制；生产测试应通过同一 HTTPS 网站地址进行。

升级前已存在但没有来源/设备记录的旧会话，可继续用于原有后台功能；在线测试需要重新登录一次建立来源绑定。绑定后的同一会话仍由前后台共同使用，不需要分别登录。

退出会话会阻止后续认证及未使用的测试授权，不会撤销已经开始或完成的业务操作。管理员清理会话前请确认用户和设备；对于异常来源，可先退出该会话，再调整账号权限或密码。

### Cloudflare 缓存边界

仅允许缓存内容哈希命名的 `/_next/static/*` 静态资源。禁止缓存 `/auth/*`、`/account/*`、`/test/*`、`/admin/v1/*`、管理 UI、`/api/*`、`/metrics`、健康检查与包含 `Set-Cookie` 的响应。`/catalog.json` 与首页当前使用 no-store，以保证价格、开放状态和调用域名修改立即生效；不要用“缓存所有内容”规则覆盖这些响应。OAuth 回调必须直达源站，不可缓存或改写参数。主程序的插件数据库缓存是独立的服务端功能，不代表可以缓存用户或计费 HTTP 响应。
