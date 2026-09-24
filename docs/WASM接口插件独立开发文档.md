# API Manager 独立 WASM 接口插件开发文档

> 文档版本：1.1  
> 适用运行时：API Manager WASM Runtime  
> 更新日期：2026-09-24

本文档面向希望为 API Manager 开发独立 WASM 接口插件的开发者。示例使用通用的 `inventory-demo` 插件名称，不依赖任何具体业务系统、数据库或外部服务。

---

## 1. 插件是什么

API Manager WASM 插件不是一个独立的 HTTP 服务，也不会监听端口。它是由 API Manager 网关在匹配到已发布 API 后调用的 WASM 模块：

```text
客户端请求
    │
    ▼
API Manager Gateway
    ├─ 匹配 method + path
    ├─ API Key / JWT / HMAC 鉴权
    ├─ 请求参数校验
    ├─ 限流与配额
    ├─ 审计日志、指标和链路追踪
    │
    ▼
WASM Plugin
    │
    ▼
HTTP 响应
```

必须区分以下三个概念：

| 概念 | 说明 |
| --- | --- |
| WASM 插件 | 被加载到 API Manager 运行时的代码模块。 |
| manifest 路由声明 | 插件作者声明自己支持哪些公开 API，供控制台生成建议和调用示例。 |
| API Manager API 路由 | 管理员实际创建并发布的网关路由，决定客户端能否访问插件。 |

**上传插件不会自动创建或发布 API。**完整流程是：

```text
开发 WASM 模块
  → 编写 manifest.yaml
  → 本地测试
  → 上传 manifest.yaml + WASM
  → 启用插件
  → 创建并发布 API 路由
  → 使用 API Key/JWT/HMAC 调用
```

---

## 2. 运行限制

WASM 插件运行在 wazero 沙箱中，默认不能访问：

- 宿主文件系统；
- 网络；
- 环境变量；
- PostgreSQL、Redis 等外部服务；
- Docker Socket 或其他宿主资源。

鉴权、限流、配额、审计、OpenTelemetry 和 Prometheus 指标由 API Manager 网关负责。插件只负责处理请求并返回业务响应，不要在插件中重复实现 API Key 校验或直接连接数据库。

插件执行受 manifest 中的超时和内存限制约束。每次请求体由宿主最多读取 1 MiB。

---

## 3. 开发环境

推荐使用：

- Go 1.26 或兼容 `wasip1/wasm` 的 Go 版本；
- `GOOS=wasip1`；
- `GOARCH=wasm`；
- `CGO_ENABLED=0`；
- API Manager 本地运行环境，用于上传和端到端测试。

如果使用其他语言，只要最终生成的 WASM 模块满足本文档规定的 ABI，也可以接入。

---

## 4. 插件目录结构

最小插件包如下：

```text
inventory-demo/
├── manifest.yaml
└── plugin.wasm
```

`manifest.yaml` 中的 `entrypoint` 必须是当前目录内的 `.wasm` 文件名：

```yaml
entrypoint: plugin.wasm
```

以下写法会被拒绝：

```yaml
entrypoint: ../plugin.wasm
entrypoint: /tmp/plugin.wasm
entrypoint: subdir/plugin.wasm
```

插件包中可以携带其他说明文件。上传时提交 `manifest.yaml` 和 WASM 模块即可。

---

## 5. manifest.yaml

### 5.1 完整示例

```yaml
id: example.api.inventory
name: inventory-demo
version: 1.0.0
api_version: v1
runtime: wasm
entrypoint: plugin.wasm

limits:
  timeout_ms: 3000
  memory_mb: 64

routes:
  - name: 服务状态
    method: GET
    path: /api/inventory-demo/v1/status
    auth_mode: api_key

  - name: 查询商品
    method: GET
    path: /api/inventory-demo/v1/items/{external_id}
    auth_mode: api_key
    example_path_params:
      external_id: example-id
    example_query:
      region: example-region
```

### 5.2 字段说明

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `id` | 否 | 插件稳定标识，建议使用反向域名格式。 |
| `name` | 是 | 插件名称。创建 API 时，API 的 `plugin` 字段必须精确等于此值。 |
| `version` | 是 | 插件版本。同一个 `name + version` 不能重复上传。 |
| `api_version` | 否 | 插件业务接口版本或 ABI 版本。 |
| `runtime` | 是 | 当前必须为 `wasm`。 |
| `entrypoint` | 是 | 包内 `.wasm` 文件名，不能包含目录。 |
| `capabilities` | 否 | 仅接受 `database_write` 声明；这是将来可控写入的预留能力，当前没有 WASM host ABI。默认不开启。 |
| `limits.timeout_ms` | 否 | 单次执行超时时间。未填写或为 `0` 时使用 3000 毫秒。 |
| `limits.memory_mb` | 否 | WASM 线性内存上限，单位 MiB。`0` 表示不额外设置。 |
| `routes` | 否 | 插件支持的公开 API 声明，不会自动创建或发布 API。 |
| `routes[].name` | 否 | 控制台中的显示名称。 |
| `routes[].method` | 是 | 必须是大写的 `GET`、`POST`、`PUT`、`PATCH`、`DELETE`、`HEAD` 或 `OPTIONS`。 |
| `routes[].path` | 是 | 最终公开路径，必须以 `/api/` 开头，不能包含 `//`、查询字符串或空白。 |
| `routes[].auth_mode` | 是 | `none`、`api_key`、`jwt` 或 `hmac`。 |
| `routes[].example_path_params` | 否 | 路径占位符的示例值。 |
| `routes[].example_query` | 否 | 控制台生成 curl 示例时使用的查询参数。 |

### 5.3 路由声明规则

例如：

```yaml
path: /api/inventory-demo/v1/items/{external_id}
example_path_params:
  external_id: example-id
```

其中 `example_path_params` 的键必须和路径占位符一致。

以下示例不合法：

```yaml
# 缺少 /api/ 前缀
path: /v1/status

# 包含连续斜线
path: /api//inventory/v1/status

# 把查询字符串写入 path
path: /api/inventory/v1/items?region=US

# 占位符名称不合法
path: /api/inventory/v1/items/{item-id}
```

### 5.4 路由声明不是发布配置

`routes` 只表示“插件作者声称实现了这些接口”。控制台会根据它：

- 展示建议路由；
- 生成调用示例；
- 预填接口创建表单；
- 显示方法和鉴权模式。

它不会自动：

- 创建数据库中的 API；
- 发布 API；
- 创建调用凭证；
- 绕过 API Manager 鉴权；
- 验证插件业务代码是否真的实现了该路径。

### 5.5 版本升级

已经上传的插件记录不会因为本地 manifest 修改而自动更新。升级步骤：

1. 修改 `manifest.yaml` 或 WASM 代码；
2. 提高 `version`，例如从 `1.0.0` 改为 `1.0.1`；
3. 提高版本号并重新上传 manifest 和 WASM；
4. 启用新版本；
5. 检查并发布对应 API。

同名插件启用新版本时，系统会自动停用同名旧版本。删除插件前必须先禁用它。

旧版本没有 `routes` 也可以继续运行，但控制台不会为它猜测接口，只展示已经实际绑定的 API。

---

## 6. WASM ABI

### 6.1 必须导出的内容

WASM 模块必须导出：

```text
memory
alloc(size: i32) -> i32
handle(request_ptr: i32, request_len: i32) -> i64
```

API Manager 在上传和启用时都会验证这些导出。

### 6.2 调用过程

宿主执行以下步骤：

1. 构造请求 JSON；
2. 调用 `alloc` 申请 WASM 线性内存；
3. 将请求 JSON 写入返回的内存地址；
4. 调用 `handle(request_ptr, request_len)`；
5. 从 `handle` 返回值中解析响应指针和响应长度；
6. 从 WASM 内存读取响应 JSON；
7. 将响应转换为 HTTP 响应。

`handle` 的 `i64` 返回值使用以下布局：

```text
高 32 位：响应 JSON 指针
低 32 位：响应 JSON 长度
```

等价伪代码：

```text
response_ptr = uint32(result >> 32)
response_len = uint32(result)
```

### 6.3 宿主传入的请求 JSON

```json
{
  "method": "GET",
  "path": "/api/inventory-demo/v1/status",
  "query": {
    "region": ["US"]
  },
  "headers": {
    "accept": ["application/json"]
  },
  "body_base64": "",
  "api_id": "api_01JXXXXXXXXXXXXXXX"
}
```

字段说明：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `method` | string | 原始 HTTP 方法。 |
| `path` | string | 原始请求路径。 |
| `query` | object | 查询参数，值始终是字符串数组。 |
| `headers` | object | 请求头，值始终是字符串数组。不要记录敏感头内容。 |
| `body_base64` | string | 请求体 Base64 编码，最多 1 MiB。 |
| `api_id` | string | 当前匹配的 API 配置 ID。 |

请求路径是客户端请求的完整公开路径，不会自动转换为 `/v1/...`。插件应根据自己的 manifest 路由处理完整路径。

### 6.4 插件返回的响应 JSON

```json
{
  "status": 200,
  "headers": {
    "Content-Type": ["application/json"]
  },
  "body_base64": "eyJvayI6dHJ1ZX0="
}
```

`body_base64` 解码后是实际 HTTP 响应体，例如：

```json
{"ok":true}
```

注意：

- `status` 为 `0` 时，宿主按 `200` 处理；
- `headers` 中的每个值都会写入 HTTP 响应；
- `body_base64` 必须是合法 Base64；
- 业务错误应通过状态码表达，例如 `400`、`404` 或 `409`；
- WASM 执行失败会由网关转换为 `502`，并记录插件失败日志和指标。

---

## 7. Go 最小插件示例

以下代码展示一个可以返回 JSON 的最小插件骨架。实际项目应增加完整的路径、参数和业务错误处理。

```go
package main

import (
    "encoding/base64"
    "encoding/json"
    "net/http"
    "strings"
    "unsafe"
)

type wasmRequest struct {
    Method  string              `json:"method"`
    Path    string              `json:"path"`
    Query   map[string][]string `json:"query"`
    Headers map[string][]string `json:"headers"`
    Body    string              `json:"body_base64"`
    APIID   string              `json:"api_id"`
}

type wasmResponse struct {
    Status  int                 `json:"status"`
    Headers map[string][]string `json:"headers"`
    Body    string              `json:"body_base64"`
}

var requestBuffer []byte
var responseBuffer []byte

//go:wasmexport alloc
func alloc(size uint32) uint32 {
    // 请求体最多读取 1 MiB，但 Base64 与 JSON 会增加实际 ABI 请求大小。
    if size == 0 || size > 4<<20 {
        return 0
    }
    requestBuffer = make([]byte, size)
    return uint32(uintptr(unsafe.Pointer(&requestBuffer[0])))
}

//go:wasmexport handle
func handle(ptr uint32, length uint32) uint64 {
    if length == 0 || length > 4<<20 || len(requestBuffer) < int(length) ||
        ptr != uint32(uintptr(unsafe.Pointer(&requestBuffer[0]))) {
        return encodeResponse(http.StatusBadRequest, map[string]any{
            "error": "invalid wasm request",
        })
    }

    var req wasmRequest
    if err := json.Unmarshal(requestBuffer[:length], &req); err != nil {
        return encodeResponse(http.StatusBadRequest, map[string]any{
            "error": "invalid request JSON",
        })
    }

    if req.Method != http.MethodGet {
        return encodeResponse(http.StatusMethodNotAllowed, map[string]any{
            "error": "method not allowed",
        })
    }

    if req.Path == "/api/inventory-demo/v1/status" {
        return encodeResponse(http.StatusOK, map[string]any{
            "status": "ok",
            "plugin": "inventory-demo",
        })
    }

    const itemPrefix = "/api/inventory-demo/v1/items/"
    if strings.HasPrefix(req.Path, itemPrefix) {
        id := strings.TrimPrefix(req.Path, itemPrefix)
        if id == "" || strings.Contains(id, "/") {
            return encodeResponse(http.StatusNotFound, map[string]any{
                "error": "item not found",
            })
        }
        if region := req.Query["region"]; len(region) > 0 && region[0] != "example-region" {
            return encodeResponse(http.StatusBadRequest, map[string]any{
                "error": "unsupported region",
            })
        }
        return encodeResponse(http.StatusOK, map[string]any{
            "external_id": id,
            "region": "example-region",
        })
    }

    return encodeResponse(http.StatusNotFound, map[string]any{
        "error": "route not found",
    })
}

func encodeResponse(status int, payload any) uint64 {
    body, err := json.Marshal(payload)
    if err != nil {
        body = []byte(`{"error":"encode response failed"}`)
        status = http.StatusInternalServerError
    }

    encoded, err := json.Marshal(wasmResponse{
        Status: status,
        Headers: map[string][]string{
            "Content-Type": {"application/json"},
        },
        Body: base64.StdEncoding.EncodeToString(body),
    })
    if err != nil {
        return 0
    }

    responseBuffer = encoded
    if len(responseBuffer) == 0 {
        return 0
    }

    responsePtr := uint32(uintptr(unsafe.Pointer(&responseBuffer[0])))
    return uint64(responsePtr)<<32 | uint64(len(responseBuffer))
}

func main() {}
```

开发时应注意：

- 不要在插件中打印 API Key、JWT、Cookie 或完整请求头；
- 不要保存跨请求的敏感状态；
- 不要返回宿主内存地址以外的无效指针；
- `alloc` 必须能容纳 Base64 扩大后的完整请求 JSON，不能简单地把上限设为原始请求体的 1 MiB；
- 所有返回体都应通过 `body_base64` 编码；
- 不要让插件因为未知路径崩溃，应返回明确的 `404`。

---

## 8. 构建 WASM

在插件项目根目录执行：

```bash
GOOS=wasip1 \
GOARCH=wasm \
CGO_ENABLED=0 \
go build \
  -buildmode=c-shared \
  -trimpath \
  -o plugin.wasm \
  .
```

如果插件源码位于 `cmd/wasm-plugin`：

```bash
GOOS=wasip1 \
GOARCH=wasm \
CGO_ENABLED=0 \
go build \
  -buildmode=c-shared \
  -trimpath \
  -o plugin.wasm \
  ./cmd/wasm-plugin
```

构建后确认文件存在：

```bash
ls -lh plugin.wasm
file plugin.wasm
```

可选的导出检查：

```bash
wasm-tools print plugin.wasm | grep -E 'memory|alloc|handle'
```

至少应确认存在：

```text
memory
alloc
handle
```

---

## 9. 本地测试

### 9.1 API Manager 单元测试

在 API Manager 项目根目录执行：

```bash
go test ./...
```

### 9.2 插件自身测试

插件应至少覆盖：

- 合法请求；
- 不支持的 HTTP 方法；
- 未知路径；
- 缺少路径参数；
- 无效查询参数；
- 错误 JSON；
- 响应 JSON 序列化失败；
- 边界输入和超时场景。

### 9.3 端到端测试

建议至少验证：

```text
上传 manifest + plugin.wasm
  → 启用插件
  → 创建 API
  → API 的 plugin 等于 manifest.name
  → 发布 API
  → 通过网关访问公开路径
  → 验证状态码、响应头和响应体
```

---











---

## 10. 上传插件

### 10.1 通过控制台

1. 登录 API Manager 控制台；
2. 打开“插件”页面；
3. 选择 `manifest.yaml`；
4. 选择 `plugin.wasm`；
5. 点击“上传插件”；
6. 上传成功后点击“启用”；
7. 在“接口与调用”查看 manifest 声明的路由；
8. 点击“创建接口”，或到“接口管理”手动配置；
9. 确认并发布 API。

### 10.2 通过管理 API

先设置环境变量：

```bash
export API_MANAGER=http://localhost:8080
export ADMIN_TOKEN='替换为管理员 Token'
```

上传：

```bash
curl -X POST "$API_MANAGER/admin/v1/plugins" \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -F 'manifest=@manifest.yaml' \
  -F 'wasm=@plugin.wasm' \
```

响应中会包含插件 `id`。上传后默认禁用，启用：

```bash
curl -X PUT "$API_MANAGER/admin/v1/plugins/<PLUGIN_ID>/status" \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"enabled":true}'
```

管理接口需要相应的 `plugin.manage` 权限。生产环境不要把 Token 写入脚本、仓库或日志。

---

## 11. 创建和发布 API 路由

manifest 中的 `routes` 不会自动写入数据库。可以通过控制台创建，也可以调用管理 API。

示例：

```bash
curl -X POST "$API_MANAGER/admin/v1/apis" \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "Inventory status",
    "method": "GET",
    "path": "/api/inventory-demo/v1/status",
    "auth_mode": "api_key",
    "plugin": "inventory-demo"
  }'
```

注意：`plugin` 必须与 manifest 中的 `name` 完全一致，包括大小写、下划线和连字符。例如：

```text
inventory-demo != Inventory_Demo
```

创建接口后，使用返回的 API ID 发布：

```bash
curl -X POST "$API_MANAGER/admin/v1/apis/<API_ID>/publish" \
  -H "X-Admin-Token: $ADMIN_TOKEN"
```

发布前确认：

- API 的 `method` 与 manifest 路由一致；
- API 的 `path` 与 manifest 路由一致；
- API 的 `plugin` 与 manifest 的 `name` 完全一致；
- 插件接口不应填写无关的 `upstream_url`；当前网关在 `plugin` 非空时优先执行插件，请避免混淆配置；
- API 已启用并已发布；
- 插件版本处于启用状态。

---

## 12. 调用插件 API

如果 API 使用 API Key 鉴权：

```bash
export API_KEY='替换为调用凭证'

curl -X GET \
  "$API_MANAGER/api/inventory-demo/v1/status" \
  -H "X-API-Key: $API_KEY"
```

如果 API 使用 JWT：

```bash
curl -X GET \
  "$API_MANAGER/api/inventory-demo/v1/status" \
  -H "Authorization: Bearer $JWT"
```

如果 API 使用 HMAC，需要按照 API 的 HMAC 配置计算签名请求头。插件不负责验证这些网关鉴权信息。

典型响应：

```json
{
  "status": "ok",
  "plugin": "inventory-demo"
}
```

---

### 12.1 本地插件库


| 方法 | 路径 | 权限 | 作用 |
| --- | --- | --- | --- |
| POST | `/admin/v1/plugin-library` | `plugin.manage` | 请求体 `{"plugin_id":"<PLUGIN_ID>"}`，加入插件库 |

例子：

```bash
curl -X POST "$API_MANAGER/admin/v1/plugin-library" \
  -H "X-Admin-Token: $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"plugin_id":"<PLUGIN_ID>"}'
curl -H "X-Admin-Token: $ADMIN_TOKEN" "$API_MANAGER/admin/v1/plugin-library"
curl -X POST "$API_MANAGER/admin/v1/plugin-library/install?name=inventory-demo&version=1.0.0" \
  -H "X-Admin-Token: $ADMIN_TOKEN"
```

### 12.2 数据库写入预留（当前不可供 WASM 使用）

可选的 `capabilities: [database_write]` 声明经过 manifest 校验；默认 `PLUGIN_DATABASE_WRITES_ENABLED=false` 时，声明此能力的插件不能启用。代码已提供 `plugin_data` 表和主机侧 `DatabaseWriter` 边界，使用插件名、命名空间和键定位最多 1 MiB 的 JSON 值，默认拒绝、无任意 SQL 权限。**当前没有暴露给 WASM 的 host ABI**，也没有将该 writer 接入插件运行时；即使设置环境变量为 `true`，插件仍不能实际读写数据库。后续开放时必须绑定由宿主确定的插件身份，并增加配额、审计、隔离和授权测试，切勿信任 WASM 自报的 `plugin_name`；主机侧 writer 强制要求宿主绑定 `PluginName`，未来也必须由宿主填充后再暴露接口。数据目前按插件名称（非版本）保存，卸载插件不会清除此表；正式开放前应制定数据保留、配额和跨版本迁移策略。

---

## 13. 安全与生产建议

### 插件代码

- 不要在日志中输出 API Key、JWT、Cookie 或完整请求头；
- 不要依赖宿主机文件、网络或环境变量；
- 对请求体和查询参数设置业务级大小限制；
- 对所有未知路径返回 `404`；
- 对不支持的方法返回 `405`；
- 对非法参数返回 `400`；
- 避免在模块全局变量中保存用户敏感数据；
- 为每个版本生成可追踪的 checksum；
- 发布前执行静态检查和 WASM ABI 检查。

### API Manager 配置

- 生产环境修改默认 `ADMIN_TOKEN`；
- 使用 HTTPS 或反向代理终止 TLS；
- 为调用 API 配置 API Key、JWT 或 HMAC；
- 配置合理的每分钟限流和日/月配额；
- 使用审计日志追踪上传、启用、禁用和删除操作；
- 通过 Prometheus、Grafana 和 OpenTelemetry 观察错误率、延迟和插件失败；
- 插件接口不要填写无关的 `upstream_url`：当前网关会优先执行插件，混合配置容易造成误解；
- 插件目录使用持久化卷，并限制宿主机文件权限。

---

## 14. 常见问题

### 14.1 上传时报 `plugin manifest must include...`

检查 manifest 是否包含：

```yaml
name: inventory-demo
version: 1.0.0
runtime: wasm
entrypoint: plugin.wasm
```

### 14.2 上传时报 `plugin entrypoint must be a local .wasm filename`

`entrypoint` 只能是文件名，不能包含 `../`、绝对路径或目录：

```yaml
entrypoint: plugin.wasm
```

### 14.3 上传时报 `plugin must export memory...`

WASM 没有正确导出：

```text
memory
alloc
handle
```

请检查编译目标、导出注释和最终 WASM 文件，而不是只检查源码中是否存在同名函数。

### 14.4 控制台没有显示建议接口

可能原因：

- manifest 没有 `routes`；
- 上传的是旧版本，数据库中保存的 manifest 还没有 routes；
- 新版本没有启用；
- `routes` 校验失败导致上传未成功。

控制台不会再根据插件名称猜测 `/v1/status`。

### 14.5 点击调用返回 `404 api route not found`

检查：

- API 是否已经发布；
- method 和 path 是否匹配；
- API 的 `plugin` 是否与 manifest 的 `name` 完全一致；
- 当前服务连接的数据库是否就是创建 API 的数据库。

如果路由已发布但插件未启用或 `plugin` 名称不匹配，网关会返回插件未找到的服务器错误，请单独检查插件状态。

### 14.6 返回 `502 plugin execution failed`

这通常表示 WASM 执行或响应解码失败。检查：

- `alloc` 是否返回有效指针；
- `handle` 是否读取了正确长度；
- 返回值高低 32 位是否正确编码；
- 响应 JSON 是否合法；
- `body_base64` 是否是合法 Base64；
- 插件是否超时或触发内存限制。

### 14.7 返回 `401` 或 `403`

这是网关鉴权或 RBAC 结果，不是 WASM ABI 错误。检查：

- API 的鉴权模式；
- `X-API-Key` 或 `Authorization` 请求头；
- 调用凭证是否被撤销或过期；
- 当前管理用户是否有对应权限。

---

## 15. 发布前检查清单

### manifest

- [ ] `name` 唯一且与 API 的 `plugin` 字段一致；
- [ ] `version` 已更新；
- [ ] `runtime: wasm`；
- [ ] `entrypoint` 是包内 `.wasm` 文件名；
- [ ] `routes` 使用真实实现的 method/path；
- [ ] 路由没有重复、连续斜线或查询字符串；
- [ ] 示例路径参数和查询参数真实可用。

### WASM

- [ ] 导出 `memory`；
- [ ] 导出 `alloc(i32) -> i32`；
- [ ] 导出 `handle(i32, i32) -> i64`；
- [ ] 处理错误 JSON；
- [ ] 处理未知路径和不支持的方法；
- [ ] 响应符合 ABI；
- [ ] 不记录敏感请求信息；
- [ ] 在超时和边界输入下不会崩溃。

### API Manager

- [ ] 插件三文件上传成功；
- [ ] 插件已启用；
- [ ] API 已创建；
- [ ] API 的 plugin 名称精确匹配；
- [ ] API 已发布；
- [ ] 调用凭证有效；
- [ ] 已完成一次带鉴权的端到端调用；
- [ ] 已检查审计日志、指标和链路追踪。
