# 游戏折扣项目接入

接入源项目：`/Volumes/SSD/项目/game-discount-api`。

保留独立业务服务，通过 API Manager 的 HTTP 上游代理接入，不复制业务实现或绕过其鉴权。统一的调用 Key、限流、发布快照、审计、网关指标及客户端 Trace 由 API Manager 提供。游戏服务继续负责数据契约、分页、快照和数据校验。

## 路由

| API Manager 入口（GET） | 游戏服务上游 |
|---|---|
| `/api/game-discount/v1/offers` | `/v1/offers` |
| `/api/game-discount/v1/offers/{external_id}` | `/v1/offers/{external_id}` |
| `/api/game-discount/v1/status` | `/v1/status` |

列表调用必须指定 `region=HK`。支持 `limit`、`page_cursor`、`updated_since`、`sync_cursor`。查询字符串和条件请求头透传；ETag/304、400/404/410/429 保留。未匹配网关路由或网关鉴权/限流拒绝时仍使用 API Manager 的错误格式。

## Docker 部署

从 API Manager 目录执行：

```bash
cd /Volumes/SSD/项目/api
# 如果已有管理员 Token，请先设置 API_MANAGER_ADMIN_TOKEN，避免切换管理 Token。
make game-discount-init
make game-discount-up
# 等待 http://localhost:8080/health/ready 返回 ready 后：
make game-discount-register
# 创建一个调用 Key，明文只写到权限 0600 的文件（文件存在时拒绝覆盖）：
python3 scripts/register-game-discount.py --publish \
  --client-key-file integrations/game-discount/client.key
```

初始化生成 `integrations/game-discount/.env`（0600、已加入忽略规则），包含独立随机的网关管理 Token、游戏服务管理 Token、服务间 Key 和游戏数据库密码。不会打印或覆盖已存在的机密。不要提交这个文件或运行会打印完整环境变量的命令到公开日志。已有非简单字符 Token 可以自行安全配置 env 文件；初始化脚本只接受字母、数字、`-`、`_`。

组合 Compose 文件：

```bash
docker compose --env-file integrations/game-discount/.env \
  -f compose.yaml -f compose.game-discount.yaml ps
```

- 默认从相邻目录 `../game-discount-api` 构建，可以在私有 env 文件设置 `GAME_DISCOUNT_PROJECT_PATH` 覆盖。
- 游戏服务和其 PostgreSQL 只加入内部网络，不映射宿主机端口；游戏管理后台不会通过网关注册。
- 游戏数据库独立持久化到 `game_discount_postgres_data`，不复用 API Manager 的表。
- **报价本身仍由 `FEED_FILE` 加载，默认是原项目的示例文件，不是实时抓取商店数据。** 正式数据需要用 Compose 覆盖文件只读挂载授权 feed，再设置 `FEED_FILE`；更新文件后重启游戏服务重新加载。
- 游戏服务默认聚合预算为 6000 次/分钟，未认证 IP 预算 12000；网关默认每个 API、每个调用 Key 120 次/分钟，按需调整。
- 标准 Compose 的其它默认密码不在本次接入范围，生产部署仍需配置。

## 调用

```bash
KEY="$(cat integrations/game-discount/client.key)"
curl -H "X-API-Key: $KEY" \
  'http://localhost:8080/api/game-discount/v1/offers?region=HK&limit=10'
curl -H "X-API-Key: $KEY" \
  'http://localhost:8080/api/game-discount/v1/offers/example-001'
curl -H "Authorization: Bearer $KEY" \
  'http://localhost:8080/api/game-discount/v1/status'
```

这三个接口会在管理控制台的接口列表出现，可使用现有发布/下线/限流/审计能力。调用 Key 仍遵循平台现有的全局 API Key 模型，并非只限定访问游戏接口的资源级凭证。

## 重复注册与配置维护

`routes.json` 是接入清单，通过管理 HTTP API 写入，因而产生正常审计和发布记录。不直接改数据库。

- 默认只创建草稿；`--publish` 显式发布。
- 相同配置重复执行不重复创建 API 或发布版本。
- 发现同路径的非本接入接口直接报冲突，不覆盖。
- 本接入配置有差异时也不静默覆盖，需要 `--update`；已发布接口的更新遵循平台当前行为（立即更新），操作前请先下线或在维护窗口执行。
- 注册是逐条管理请求，不是跨三条接口的数据库事务；中途失败可重新执行。
- `--dry-run` 只展示路由清单，无需密钥也不会写入。

```bash
python3 scripts/register-game-discount.py --dry-run
python3 scripts/register-game-discount.py --update --publish
```

## 凭证边界与本地接入

网关读取部署环境中的 `API_UPSTREAM_CREDENTIALS`，例如：

```json
{"game-discount":{"origin":"http://game-discount:8089","api_key":"<服务间Key>"}}
```

API 只保存 `upstream_auth_ref: game-discount`，不会把 Key 存入 API 记录、发布快照或控制台。服务端凭证绑定到具体 origin（协议/主机/端口），修改上游地址到其它 origin 会返回 502 而不会发送凭证。别名配置缺失也不会回退为匿名调用。

网关完成外部鉴权后再注入上游 `X-API-Key`，移除客户端 `Authorization`、Cookie 和管理 Token，并重建转发 IP 头。HTTP 上游 Key 不具有原服务管理权限。原服务继续校验 Key 并记录审计；`X-Request-ID` 可用于两端关联。原服务没有新增 OTel SDK Server Span，本次只复用 API Manager 的入口和上游客户端 Span。

游戏服务设置 `GATEWAY_API_KEY` 时会幂等写入固定身份 `api-manager-gateway`，数据库只保存 SHA256。更换该值并重启服务会使旧 Key 失效；网关必须同步更新自己的配置并重启。该身份可在游戏服务后台吊销，但若保留环境变量，下次启动会重新启用。长期移除接入时需同时清除变量并吊销数据库中的 Key（仅清除变量不会删除既有记录）。

已有本地游戏服务也可接入：游戏服务设置同一个 `GATEWAY_API_KEY` 并重启，API Manager 设置 `API_UPSTREAM_CREDENTIALS` 的 origin 为本地服务地址并重启，然后：

```bash
# API_MANAGER_ADMIN_TOKEN 从环境变量或 --env-file 提供，不作为命令行参数暴露
python3 scripts/register-game-discount.py --base-url http://127.0.0.1:8080 \
  --upstream-url http://127.0.0.1:8089 --publish
```

注册脚本不负责重启服务或修改正在运行的进程环境。

## 测试与验收

```bash
make game-discount-test
```

此命令编译并临时启动两个真实 Go 服务，使用隔离内存存储、临时随机端口和 Key，验证：注册及幂等发布、双层鉴权、路径映射、分页、ETag/304、错误码、请求 ID、限流、指标、上游 Key 吊销。结束时关闭服务并移除临时文件，不会操作现有数据库。

PostgreSQL 与 Docker 全栈仍需 Docker daemon 可用后验证。仅通过本地 smoke 测试不代表已完成生产部署。
