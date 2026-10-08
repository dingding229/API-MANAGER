# 插件开发规范

本文说明 API Manager 当前实现的 WebAssembly 插件契约，适用于 `v0.3.47` 的插件运行边界。前面的基础示例不需要网络或持久化。第 11 节另提供受控网络与插件会话接口。

**先明确三件事：**

1. 插件包是 `manifest.yaml` 和一个 `.wasm` 文件；上传、安装、启用和接口发布是不同操作。
2. 插件实现 `memory`、`alloc`、`handle` 导出；不是通过启动 HTTP 服务器或把输出写到 stdout 返回结果。
3. 主程序负责 API 鉴权、参数检查、计费与缓存。插件只处理传入的业务请求，不获得管理权限，也不能直接操作主程序数据库；可在授权后使用独立的插件会话存储接口。

## 目录

- [1. 运行流程与能力边界](#1-运行流程与能力边界)
- [2. 插件包与清单](#2-插件包与清单)
- [3. ABI 与内存管理](#3-abi-与内存管理)
- [4. 请求 JSON](#4-请求-json)
- [5. 响应 JSON](#5-响应-json)
- [6. 最小 Go 插件](#6-最小-go-插件)
- [7. 编译与交付](#7-编译与交付)
- [8. 安装、设置、绑定接口与发布](#8-安装设置绑定接口与发布)
- [9. 插件设置模块](#9-插件设置模块)
- [10. 主程序数据库缓存](#10-主程序数据库缓存)
- [11. 数据库写入与网络访问](#11-数据库写入与网络访问)
- [12. 插件版本与升级](#12-插件版本与升级)
- [13. 错误排查](#13-错误排查)
- [14. 发布检查清单](#14-发布检查清单)
- [15. 对照实现](#15-对照实现)

## 1. 运行流程与能力边界

典型调用流程如下：

```text
客户端 → /api/* → 主程序校验与调用结算 → 查询插件响应缓存
                                         ├─ 命中：返回缓存结果
                                         └─ 未命中：实例化 WASM → alloc → 写入请求 → handle
                                                                            ↓
                                                       读取并校验响应 JSON → 返回结果
```

插件包在安装或加载时编译、检查导出。每次实际执行插件时，主程序实例化一个新的模块；若存在 `_initialize`，会执行初始化，再调用 `alloc` 和 `handle`。执行结束后关闭该实例。

因此：

- 不要依靠模块全局变量跨 HTTP 请求保存状态。
- 不要在初始化阶段启动无法结束的循环或 HTTP 服务。
- 不要把 `main`、`_start` 或 stdout 输出当作插件返回接口。
- WASI Preview 1 提供运行时所需的时钟、随机数、标准输入等接口，但主程序没有提供预打开目录、环境变量或可用的网络连接。
- 当前只有 WASM 运行方式，清单中的 `runtime` 必须为 `wasm`。

接口与用户的权限检查、套餐用量、余额扣费及调用日志由主程序处理；命中缓存不会跳过这些步骤。插件作者不能通过返回自定义用户、角色或余额字段改变这些规则。

## 2. 插件包与清单

最小交付结构：

```text
hello-tools/
├── manifest.yaml
└── plugin.wasm
```

管理后台上传时分别选择这两个文件，不是上传 ZIP/TAR 安装包。源码可以另外保存在开发仓库中，不需要作为运行插件文件上传。

### 2.1 完整的最小清单

保存为 `manifest.yaml`，用于下文的 Go 示例：

```yaml
name: hello-tools
version: "1.0.0"
api_version: "v1"
runtime: wasm
entrypoint: plugin.wasm
capabilities: []
limits:
  timeout_ms: 3000
  memory_mb: 64
routes:
  - name: 问候接口
    method: GET
    path: /api/hello
    auth_mode: api_key
    description: 返回问候内容。
    parameters_schema:
      type: object
      additionalProperties: false
      required: [path, query, header]
      properties:
        path:
          type: object
          description: 路径参数，无参数。
          properties: {}
          additionalProperties: false
        query:
          type: object
          description: 查询参数。
          properties:
            name:
              type: string
              description: 称呼，省略时使用默认称呼。
              maxLength: 80
              examples: [Alice]
          additionalProperties: false
        header:
          type: object
          description: 无自定义请求头，允许标准 HTTP 请求头。
          properties: {}
          additionalProperties: true
    request_schema:
      type: 'null'
      description: 不发送请求正文。
    example_query:
      name: Alice
settings_schema:
  type: object
  additionalProperties: false
  properties:
    prefix:
      type: string
      title: 问候前缀
      description: 返回消息使用的前缀
      minLength: 1
      maxLength: 32
      default: 你好
  required: [prefix]
```

### 2.2 清单字段

| 字段 | 要求与作用 |
| --- | --- |
| `name` | 必填。插件逻辑名称，首字符为字母或数字，只使用字母、数字、点、下划线或短横线，总长 1–128。接口通过该名称选择插件。 |
| `version` | 必填。采用与 `name` 相同的字符和长度限制，建议使用 `1.0.0` 等语义化版本。 |
| `api_version` | 建议填写 `v1`，用于说明所采用的契约。当前实现没有按该字段做版本协商，不能以此替代 ABI 兼容性测试。 |
| `runtime` | 必填，固定为 `wasm`。 |
| `entrypoint` | 必填，仅允许当前目录内的 `.wasm` 文件名，不能是路径、URL 或父目录引用。 |
| `id` | 可省略。不要用它指定后台插件记录 ID；安装记录的 `plugin_id` 由主程序生成。 |
| `routes` | 可选，最多 100 项，用于说明建议接口。不会自动创建、授权或发布接口。 |
| `settings_schema` | 可选，声明后台可编辑的设置，详见第 9 节。 |
| `capabilities` | 可省略或为 `[]`。目前解析器只认识 `database_write`；其限制见第 11 节，不建议在普通插件中声明。 |
| `limits.timeout_ms` | 1–30000 毫秒；省略或为 0 时默认 3000。 |
| `limits.memory_mb` | 1–256 MiB；省略或为 0 时默认 64。限制的是 WASM 线性内存，不是全部宿主进程内存。 |

插件名称、版本和运行记录 ID 不是同一个概念：

- `hello-tools` 是逻辑名称，填写到接口的 `plugin` 字段。
- `1.0.0` 是插件包版本。
- `plugin_…` 是后台生成的安装记录 ID，用于启用、禁用和设置接口。

### 2.3 建议接口的约束

`routes[].method` 必须是大写的 `GET`、`POST`、`PUT`、`PATCH`、`DELETE`、`HEAD` 或 `OPTIONS`。同一清单不能重复声明相同的“请求方式 + 路径”。

`path` 必须以 `/api/` 开头，总长不超过 512，不含查询串、片段、反斜线、空白或连续 `/`。路径参数必须占据完整段，如 `/api/items/{id}`，参数名称以字母开头，只含字母、数字、下划线，且不能重复。

`auth_mode` 只允许 `api_key` 和 `none`；省略时默认 `api_key`。不要声明 JWT 或 HMAC 接口鉴权方式。

`example_path_params` 的键必须对应路径中的参数；值只能是单个安全路径段。`example_query` 的键值不能含换行，键不能含查询分隔符。

同一个业务接口可以在主程序的接口配置中选择多种请求方式；清单中的每项建议接口仍使用单个 `method`。

## 3. ABI 与内存管理

模块必须导出以下对象，名称与签名均需一致：

| 导出 | WASM 签名 | 作用 |
| --- | --- | --- |
| `memory` | 线性内存 | 主程序写入请求并读取响应。 |
| `alloc` | `(i32 size) -> i32` | 返回能够容纳 `size` 字节的请求缓冲区地址。 |
| `handle` | `(i32 request_ptr, i32 request_len) -> i64` | 处理请求，返回响应缓冲区地址和长度。 |

`handle` 的返回值打包方式：

```text
高 32 位：响应 JSON 的内存地址 response_ptr
低 32 位：响应 JSON 的字节长度 response_len

result = (uint64(response_ptr) << 32) | uint64(response_len)
```

约定：

- 请求与响应 JSON 使用 UTF-8；长度是编码后的字节数，不是字符串显示长度。
- 主程序只写入并读取指定长度，不要求 C 风格的结尾 `\0`。
- `alloc` 必须分配完整请求 JSON 所需空间。请求正文经过 Base64 编码后变大，不能把原始正文的 1 MiB 限制误当作整个请求 JSON 的长度限制。
- 响应缓冲区必须在 `handle` 返回之后仍保持有效，直至主程序完成读取；不要返回栈上临时对象的地址。
- 主程序会检查指针、长度和内存边界。返回越界位置、错误签名或无效 JSON 会导致调用失败。
- 当前没有约定 `free` 导出；一次调用结束后整个模块实例会关闭。一次调用内仍应控制分配量。

其他语言也可使用同一 ABI，但必须导出上述 WASM 对象。仅编译出普通 WASI 命令行程序或只导出 `_start`，不能替代它们。

## 4. 请求 JSON

主程序调用 `handle` 时传入如下结构：

```json
{
  "method": "GET",
  "path": "/api/hello",
  "query": {"name": ["Alice"]},
  "headers": {"Accept": ["application/json"]},
  "body_base64": "",
  "api_id": "由主程序配置的接口ID",
  "settings": {"prefix": "你好"}
}
```

| 字段 | 说明 |
| --- | --- |
| `method` | 本次 HTTP 请求方式。 |
| `path` | 实际请求路径，不是包含 `{id}` 的清单模板；路径参数需由插件按自己的路由逻辑处理。 |
| `query` | 查询参数，多值数组。例如 `?tag=a&tag=b` 对应 `{"tag":["a","b"]}`。 |
| `headers` | 业务请求头，多值数组。不应依赖头名称在客户端原始文本中的大小写。 |
| `body_base64` | 标准 Base64 编码的原始正文；无正文时为空字符串。先解码，再按接口约定解析 JSON、文本或二进制。 |
| `api_id` | 主程序的接口配置 ID，不是用户 ID，也不是插件安装 ID。 |
| `settings` | 当前启用插件版本的设置对象，可能省略或为空。秘密设置只交给对应插件实例。 |

注意：

- 忽略未来新增的未知请求字段，避免不必要的兼容性问题。
- 网页管理会话 Cookie 会在业务入口移除，但其他业务请求头仍可能包含 KEY、业务 Cookie 或第三方凭证。**不要记录或回显整个 `headers` 对象。**
- 请求 JSON 不提供可信的用户角色、余额或任意数据库访问句柄。不要信任调用者自定义的 `X-User-ID`、角色字段等身份声明。
- 主程序对原始请求正文的插件限制为 1 MiB；网关自身设置还可能更严格。

## 5. 响应 JSON

`handle` 返回的内存数据必须是下面的响应封装，而不是直接返回业务 JSON：

```json
{
  "status": 200,
  "headers": {"Content-Type": ["application/json; charset=utf-8"]},
  "body_base64": "eyJtZXNzYWdlIjoib2sifQ=="
}
```

这里的 `body_base64` 解码后为 `{"message":"ok"}`，客户端收到的是解码后的正文，不是插件封装。

要求：

- `status` 为 200–599；省略或为 0 时默认 200。需要业务错误时，返回合适的 4xx/5xx 状态和安全的错误正文。
- `headers` 的值必须是字符串数组，不能把数组写成单个字符串。
- `body_base64` 使用标准 Base64 编码；不能放置未经编码的业务正文或 data URL。
- 整个响应封装 JSON 不超过 2 MiB。正文的 Base64 字符串也不能超过 2 MiB，因此不能把它理解为“任意 2 MiB 原始响应正文”。
- 最多 64 个响应头名，每个头最多 16 个值，头名和头值的累计大小最多 16 KiB，且必须符合 HTTP 头格式，不含注入用的换行。
- 不得通过 `Set-Cookie` 写入主程序管理会话 Cookie；业务网关会过滤受保护的会话名称。
- 如果接口配置了响应 Schema，最终正文仍需通过主程序的校验。
- 不要返回内部堆栈、设置秘密、请求 KEY 或个人隐私数据作为错误消息。

## 6. 最小 Go 插件

该示例使用查询参数 `name` 和设置中的 `prefix`，只做本地计算，不访问网络或数据库。保存为 `plugin.go`：

```go
package main

import (
    "encoding/base64"
    "encoding/json"
    "unsafe"
)

var requestBuffer []byte
var responseBuffer []byte

type Request struct {
    Method     string              `json:"method"`
    Path       string              `json:"path"`
    Query      map[string][]string `json:"query"`
    Headers    map[string][]string `json:"headers"`
    BodyBase64 string              `json:"body_base64"`
    APIID      string              `json:"api_id"`
    Settings   struct {
        Prefix string `json:"prefix"`
    } `json:"settings"`
}

type Response struct {
    Status     int                 `json:"status"`
    Headers    map[string][]string `json:"headers"`
    BodyBase64 string              `json:"body_base64"`
}

func bufferAddress(buffer []byte) uint32 {
    return uint32(uintptr(unsafe.Pointer(&buffer[0])))
}

//go:wasmexport alloc
func alloc(size uint32) uint32 {
    if size == 0 {
        size = 1
    }
    requestBuffer = make([]byte, size)
    return bufferAddress(requestBuffer)
}

func reply(status int, value any) uint64 {
    body, err := json.Marshal(value)
    if err != nil {
        status = 500
        body = []byte(`{"error":"响应编码失败"}`)
    }
    response := Response{
        Status: status,
        Headers: map[string][]string{
            "Content-Type": {"application/json; charset=utf-8"},
        },
        BodyBase64: base64.StdEncoding.EncodeToString(body),
    }
    responseBuffer, err = json.Marshal(response)
    if err != nil {
        // 封装只包含整数、字符串和字符串数组，通常不会失败。
        responseBuffer = []byte(`{"status":500,"headers":{},"body_base64":""}`)
    }
    return uint64(bufferAddress(responseBuffer))<<32 | uint64(len(responseBuffer))
}

//go:wasmexport handle
func handle(requestPtr, requestLen uint32) uint64 {
    // 只读取 alloc 创建的请求缓冲区，不解引用任意外部地址。
    if len(requestBuffer) == 0 || requestPtr != bufferAddress(requestBuffer) ||
        uint64(requestLen) > uint64(len(requestBuffer)) {
        return reply(400, map[string]string{"error": "请求内存无效"})
    }
    var request Request
    if err := json.Unmarshal(requestBuffer[:requestLen], &request); err != nil {
        return reply(400, map[string]string{"error": "请求格式无效"})
    }
    if request.Method != "GET" {
        return reply(405, map[string]string{"error": "此插件仅支持 GET"})
    }
    name := "访客"
    if values := request.Query["name"]; len(values) > 0 && values[0] != "" {
        name = values[0]
    }
    if len([]rune(name)) > 64 {
        return reply(400, map[string]string{"error": "name 不能超过 64 个字符"})
    }
    prefix := request.Settings.Prefix
    if prefix == "" {
        prefix = "你好"
    }
    return reply(200, map[string]string{"message": prefix + "，" + name})
}

func main() {}
```

本例把请求和响应缓冲区保存在模块全局变量中，仅保证当前调用内的有效性，并不在请求之间保存数据。示例中的 `unsafe` 只用于取得 WASM 线性内存中的地址；请求读取使用已经分配的 Go 切片。

## 7. 编译与交付

### 7.1 Go 编译

该示例需要支持 `//go:wasmexport` 和 WASI reactor 的 Go 工具链，最低为 Go 1.24。推荐使用与项目相近的 Go 1.26 系列进行开发和验证。

在只含上述示例源文件的独立开发目录执行：

```bash
GOOS=wasip1 GOARCH=wasm go build \
  -buildmode=c-shared \
  -trimpath \
  -o plugin.wasm plugin.go
```

不要使用 `GOOS=js` 构建浏览器插件，也不要省略 reactor 的构建方式，误把仅供 `_start` 执行的命令行程序当作插件。

交付前记录校验值：

```bash
shasum -a 256 plugin.wasm
# Linux 也可使用：sha256sum plugin.wasm
```

如果使用本地插件库，可额外交付 `plugin.wasm.sha256`，内容只写二进制的 SHA-256 十六进制摘要。侧车名必须与实际 `entrypoint` 对应，如 `worker.wasm.sha256`。

### 7.2 包大小与运行限制

| 项目 | 当前限制 |
| --- | --- |
| `manifest.yaml` | 最多 256 KiB。 |
| 清单与 WASM 文件合计 | 默认最多 20 MiB；由服务器 `PLUGIN_MAX_BYTES` 配置，允许 1–100 MiB。 |
| 设置 Schema / 设置 JSON | 各最多 32 KiB，设置深度最多 16。 |
| 建议接口数 | 最多 100。 |
| WASM 线性内存 | 默认 64 MiB，允许 1–256 MiB。 |
| 单次插件执行时间 | 默认 3000 ms，允许 1–30000 ms。 |
| 原始请求正文 | 插件处理最多 1 MiB，还受网关正文限制约束。 |
| 返回响应 JSON 封装 | 最多 2 MiB。 |

`timeout_ms` 覆盖本次模块实例化、初始化与执行所在的调用上下文。超时、运行时陷阱或越界不是正常业务响应；主程序会将其作为调用失败处理。

## 8. 安装、设置、绑定接口与发布

推荐先在独立测试环境完成验证，不在生产库试验新插件。

1. 在管理后台“插件”上传 `manifest.yaml` 和 `plugin.wasm`，或从服务器本地插件库安装。
2. 新安装记录默认停用。如果声明了设置，先打开“设置”，填写必填项并保存。
3. 启用插件。启用时检查包内容、二进制校验值、ABI 和必填设置。
4. 在“接口管理”建立接口，选择逻辑插件名 `hello-tools`，设置路径 `/api/hello`、请求方式 `GET` 和需要的鉴权方式。
5. 需要公开文档及在线测试时，按网站的接口展示规则配置，并发布接口。
6. 在有测试权限的测试账号中验证请求、响应、错误路径与权限，再通过真实程序调用验证 KEY。

清单中的 `routes` 只是建议说明，**上传或启用不会自动发布接口**。

用于该示例的主程序接口配置片段如下；完整接口的编辑以当前后台表单为准：

```json
{
  "name": "问候接口",
  "path": "/api/hello",
  "method": "GET",
  "methods": ["GET"],
  "auth_mode": "api_key",
  "plugin": "hello-tools",
  "response_status": 200
}
```

### 8.1 管理接口参考

以下接口使用**管理登录会话**，不是业务 `X-API-Key`。还需相应插件权限；修改类浏览器请求必须通过本站请求保护（同源、`X-API-Request: 1`）。管理后台另有角色与共享插件管理限制，不能仅凭知道 `plugin_id` 操作插件。

| 接口 | 用途 |
| --- | --- |
| `GET /admin/v1/plugins` | 安装记录列表，要求 `plugin.read`。 |
| `POST /admin/v1/plugins` | Multipart 上传，文件字段名为 `manifest` 和 `wasm`，要求 `plugin.manage`。 |
| `PUT /admin/v1/plugins/{plugin_id}/status` | 提交 `{"enabled":true}` 或 `{"enabled":false}`，启用或停用。 |
| `DELETE /admin/v1/plugins/{plugin_id}` | 删除已停用的安装记录。不会自动删除关联接口。 |
| `GET /admin/v1/plugins/{plugin_id}/settings` | 查看设置 Schema 和脱敏值。 |
| `PUT /admin/v1/plugins/{plugin_id}/settings` | 保存设置，详见下一节。 |
| `GET /admin/v1/plugin-library` | 列出服务器本地插件库中可用的包。 |
| `POST /admin/v1/plugin-library` | 提交 `{"plugin_id":"已安装记录ID"}`，复制已验证的包进入本地插件库。 |
| `POST /admin/v1/plugin-library/install?name=hello-tools&version=1.0.0` | 从本地插件库安装；安装后仍需启用。 |

本地插件库的布局为 `插件名/版本/manifest.yaml` 与同目录 WASM 文件。加入或安装插件库是服务器本地操作，当前没有从任意远程 URL 拉取包的安装功能。

### 8.2 外部程序调用示例

`BASE_URL` 应填写测试环境的实际 API 调用地址；如果网站设置了接口专用域名，必须使用该地址。生产环境通过 HTTPS 调用。

```bash
# 以下地址仅表示本机测试环境，不会操作正式服务器。
BASE_URL='http://127.0.0.1:8080'
# 先在测试环境生成调用凭据，再安全地设置 API_KEY；不要把真实密钥写进文档或仓库。
curl --get "$BASE_URL/api/hello" \
  --data-urlencode 'name=Alice' \
  -H "X-API-Key: $API_KEY"
```

预期 HTTP 200，正文为：

```json
{"message":"你好，Alice"}
```

只有明确将接口设置为 `none` 时才省略 `X-API-Key`。网页登录 Cookie 不是外部程序调用凭据。

## 9. 插件设置模块

### 9.1 Schema 示例

需要可配置参数和秘密字段时，在清单中声明 `settings_schema`：

```yaml
settings_schema:
  type: object
  additionalProperties: false
  properties:
    region:
      type: string
      title: 地区
      enum: [CN, US, JP]
      default: CN
    timeout:
      type: integer
      title: 业务计算超时提示
      minimum: 1
      maximum: 30
      default: 5
    access_token:
      type: string
      title: 业务凭证
      description: 仅供插件处理自己的业务数据
      writeOnly: true
  required: [region, timeout]
```

这里的 `timeout` 只是业务设置示例，**不会更改宿主的 `limits.timeout_ms`**。`access_token` 也不会因此授予网络能力。

Schema 与数据要求：

- 根必须为 `type: object`，并显式设置 `additionalProperties: false`。
- Schema 和最终设置 JSON 各最多 32 KiB；设置对象深度最多 16。
- 不支持 `$ref`、`$dynamicRef` 等外部/动态引用；禁止 `__proto__`、`constructor`、`prototype` 等字段。
- `title`、`description` 是文本说明，不是可执行 HTML。
- 秘密字段必须是 `type: string`，并标记 `writeOnly: true`；不得为该字段设置 `default`。
- 当前默认值从根层 `properties` 读取，不应依赖嵌套默认值自动补全。需要嵌套必填结构时，提供完整的安全根层默认对象或要求管理员填写。
- 必填项无合法值时，插件不能启用。

### 9.2 读取与保存

读取设置响应结构：

```json
{
  "schema": {
    "type": "object",
    "additionalProperties": false,
    "properties": {
      "region": {"type":"string","enum":["CN","US","JP"],"default":"CN"},
      "timeout": {"type":"integer","minimum":1,"maximum":30,"default":5},
      "access_token": {"type":"string","writeOnly":true}
    },
    "required": ["region","timeout"]
  },
  "values": {"region":"CN","timeout":5},
  "version": 1,
  "secret_fields_set": ["access_token"]
}
```

`writeOnly` 值不回显，嵌套秘密字段也会脱敏。`secret_fields_set` 当前列举已设置的根层秘密字段，不应把它当作所有嵌套字段的完整列表。

保存请求：

```json
{
  "version": 1,
  "changes": {"region":"JP","timeout":8},
  "remove_fields": []
}
```

- `version` 使用刚读取的设置版本；首次尚未保存时可能为 0，不要硬编码示例中的 1。版本冲突时重新读取，不要用旧页面覆盖他人修改。
- 省略字段保留已有值；嵌套对象按字段合并。
- 数组中的对象按索引合并，以保留未回显的秘密。调整此类数组顺序时，应重新核对并明确填写对应秘密，不能把“未显示”理解为“空值”。
- `remove_fields` 当前删除根层字段，不是 JSON Pointer 或嵌套路径；最终对象仍须通过 Schema 校验。
- 设置按插件安装版本 ID 隔离、加密保存在主程序数据库。
- 启用插件的设置保存后，随后实例化的请求使用新设置，不必重新上传 WASM；已经取得旧设置的在途调用不会因此改写。
- 实际设置会出现在第 4 节请求的 `settings` 对象中；未声明设置的插件不需要读取这个字段。

`writeOnly` 保护后台回显，不会阻止插件把收到的秘密写入自己的输出。插件作者必须避免在响应、错误、日志或公开文档中泄露设置秘密。

## 10. 主程序数据库缓存

响应缓存是主程序能力，不需要 WASM 调用数据库。管理员可在接口管理中启用并设置：

```json
{
  "enabled": true,
  "ttl_seconds": 300,
  "max_entries": 1000,
  "cache_post": false
}
```

该对象对应接口的 `plugin_cache` 字段；有效期范围为 1–604800 秒，条目上限为 1–10000。启用时必须已经选择插件，并提供正的有效期与条目上限。

缓存规则：

- 默认适用 GET/HEAD；POST 仅在明确启用 `cache_post`、且业务允许重复请求复用响应时缓存。PUT/PATCH/DELETE 不缓存。
- 只有 HTTP 200、未超过 1 MiB、通过响应校验的结果写入缓存。
- 携带业务 Cookie、Range、条件请求，或请求要求 `no-cache` / `no-store` 时，不复用旧缓存。
- 返回 `Set-Cookie`、`Vary: *`，或返回 `Cache-Control` 中的 `private` / `no-cache` / `no-store` 时，不写入缓存。
- 缓存复用的响应头仅包括 `Content-Type`、`Content-Language`、`Content-Encoding`、`Content-Disposition`。其他自定义响应头不要依赖缓存保留。
- 缓存键区分接口配置、方法、路径、原始查询串、请求正文、业务请求头、调用身份、插件内容/设置版本和主程序构建版本。跟踪关联头不作为业务数据选择条件。
- 缓存内容加密存储。缓存命中时返回 `X-Plugin-Cache: HIT`；参与缓存但未命中时为 `MISS`。请求不符合缓存条件时可能没有该头。
- 缓存读写不可用时会执行插件或返回已生成结果；它不是插件数据持久化或执行结果“恰好一次”的保证。
- 设置变更、接口变更和插件升级会改变缓存选择；管理员也可主动清理旧缓存。
- 命中缓存仍执行鉴权、参数检查、调用计费和套餐用量检查，但不会运行插件或执行 host call。

涉及登录会话变化、外部写操作、随机数、实时信息或其他副作用的接口，应关闭接口响应缓存，或返回 `Cache-Control: no-store`，避免缓存命中跳过本次 host 操作。数据库缓存与 Cloudflare/CDN 缓存独立，不能据此把鉴权或计费 HTTP 响应设为公共 CDN 缓存。

## 11. 外部网络与插件会话

### 11.1 声明与授权

在清单中声明需要的能力，主程序仍默认关闭。仅声明能力不会自动获得权限。请求 JSON 的 `host_permissions` 对象会说明当前授予的能力，插件也可以在自己的业务设置中进一步选择是否使用；业务设置不能放宽后台授权：

```yaml
name: host-session-demo
version: 1.0.0
runtime: wasm
entrypoint: plugin.wasm
capabilities: [network, session_storage]
limits:
  timeout_ms: 5000
  memory_mb: 64
routes:
  - name: 插件会话示例
    method: GET
    path: /api/host-session-demo
    auth_mode: api_key
    description: 演示受控会话与外部请求，不返回外部登录秘密。
    parameters_schema:
      type: object
      additionalProperties: false
      properties:
        path:
          type: object
          description: 无路径参数。
          properties: {}
          additionalProperties: false
        query:
          type: object
          description: 无查询参数。
          properties: {}
          additionalProperties: false
        header:
          type: object
          description: 无自定义请求头。
          properties: {}
          additionalProperties: true
    request_schema:
      type: 'null'
      description: 不发送正文。
```

超级管理员在“插件 → 当前版本 → 能力权限”独立设置：

| 设置 | 作用 |
| --- | --- |
| 允许外部网络请求 | 只允许访问已填写的 HTTPS 来源地址；默认关闭。 |
| 来源地址 | 精确地址，如 `https://example.com`，不含路径或通配符；可填写非默认 HTTPS 端口。 |
| 请求超时 / 响应大小 | 单次请求最多 30 秒，响应最多 1 MiB；还受插件本次调用总超时约束。 |
| 会话保存到数据库 | 加密保存到主程序数据库，可在程序重启后读取；默认关闭。 |
| 会话缓存 | 在主程序内存缓存加密会话数据，不保留到下次重启；默认关闭。 |
| 会话有效期 | 默认 3600 秒，最大 30 天；插件可为每次写入选择更短的时间。 |
| 允许会话不自动过期 | 独立、默认关闭。只有持久化模式且已授权时可传 `ttl_seconds: 0`。 |
| 条目与大小限制 | 每插件最多 4096 条、单条最多 256 KiB，配置的乘积不超过 64 MiB。全局内存缓存最多 64 MiB、8192 条。 |

能力权限 HTTP 接口为 `GET /admin/v1/plugins/{plugin_id}/runtime` 和 `PUT /admin/v1/plugins/{plugin_id}/runtime`。修改需 `plugin.manage`、超级管理员身份与密码或通行密钥确认；请求为 `{"policy": {...}, "current_password": "..."}`。`policy.version` 为读取时的版本，版本冲突须刷新后重试。插件业务设置仍使用第 9 节的 `/settings`，两者不可互相覆盖。

### 11.2 Host ABI

模块可以导入：

```go
//go:wasmimport api_manager host_call
func hostCall(requestPtr, requestLen, responsePtr, responseCap uint32) uint64
```

参数均为本次模块的线性内存地址/长度。请求为 UTF-8 JSON，不超过 384 KiB；**调用前必须准备至少 2 MiB 的响应缓冲区**。返回高 32 位为响应 JSON 长度、低 32 位为 ABI 状态：`0` 成功写入响应、`1` 内存或缓冲区无效、`2` 输出编码失败。ABI 状态非零时，不可将缓冲区作为成功响应读取。

主程序先检查缓冲区，再执行操作，不允许通过“先探测响应长度、再重试”的方式重复发起有副作用的请求。有效缓冲区中的统一响应为 `{"ok":true,"result":...}` 或 `{"ok":false,"error":"..."}`。每次插件调用最多 32 次 host call；初始化函数不可发起外部请求或会话操作。

### 11.3 外部请求

```json
{"operation":"http_request","http":{"url":"https://example.com/api/data","method":"POST","headers":{"Content-Type":["application/json"]},"body_base64":"e30="}}
```

成功的 `result` 与第 5 节一样，包含 `status`、`headers`、`body_base64`。输入正文最多 256 KiB，HTTP 状态码由外部服务决定。默认不跟随重定向；收到 3xx 时，插件只能在再次通过允许地址检查后显式发起新请求。

- 只支持受控的 HTTPS HTTP 请求，不开放裸 TCP/UDP、宿主代理、任意 socket、私有 CA 或 TLS 校验跳过。
- 域名在建立连接时解析；所有解析结果必须是普通公网地址，再连接已经检查的数字地址，避免 DNS 重绑定。环回、内网、云元数据、链路本地、特殊地址及 IPv6 地址转换/隧道范围不允许访问。
- 不自动转发入站 Authorization、X-API-Key、网站登录 Cookie 或网站确认凭据。插件只能显式提交自己的外部认证信息，例如后台秘密设置中的外部 Authorization。
- Host、连接控制、代理认证和转发控制头不可覆盖。不会自动管理或跨来源发送 Cookie。
- 允许地址是授予插件的网络边界，不是业务用户授权。插件作者仍须验证外部操作的业务权限，不能把授权仅托付给来源地址检查。

### 11.4 会话操作

会话是插件自己的 JSON 数据，可保存外部服务 Cookie、访问令牌或其他状态，不是网站登录会话。命名空间由主程序绑定到**安装记录 ID**，插件不能指定其他插件的身份。

```json
{"operation":"session_get","session":{"key":"external.login","persist":true,"fresh":true}}
```

成功返回 `found`、找到时的 `value`、`version` 与可选 `expires_at`。`fresh: true` 在持久化模式下绕过内存，适用于更新前检查最新版本。未找到或已过期返回 `found:false`。

```json
{"operation":"session_put","session":{"key":"external.login","persist":true,"version":0,"ttl_seconds":3600,"value":{"cookie":"EXTERNAL_SERVICE_COOKIE"}}}
```

- `persist:true` 选择数据库；`persist:false` 选择进程缓存。省略时优先选择已授权的数据库模式，否则选择已授权的缓存模式。
- 两种模式的版本号独立，同名键不会互相覆盖。版本号是不可推断的正整数，不保证连续递增一步；必须使用读取或保存返回的版本。`version:0` 仅用于新增；修改时使用读取结果的版本，冲突时重新读取并决定是否重试。
- `ttl_seconds` 省略时使用默认时间；不可超过后台上限。`0` 仅在“允许不自动过期”及数据库保存均已开启时有效。
- 键名为 1–128 位字母、数字、点、下划线、冒号或短横线，首字符须为字母或数字。
- 保存值为合法、有限深度的 JSON，受后台大小与条目限制。数据库和内存均使用插件/键绑定的加密封装；不会保存可读的秘密值。

```json
{"operation":"session_delete","session":{"key":"external.login","persist":true,"version":1}}
```

删除也需要当前版本，避免删除其他并发请求更新后的会话。关闭能力立即拒绝后续操作并清空受影响的内存缓存；关闭不会自动删除数据库里的未过期数据。删除插件会通过数据库外键一并删除其会话。

**Cookie 使用流程**：插件发起外部登录请求 → 解析该服务返回的 Set-Cookie → 按用户、外部来源和用途选择不同的会话键 → 保存 Cookie 与对应有效期 → 后续读取并显式设置该来源请求的 Cookie 头。不得把外部 Cookie/Token 返回到公开 API、公开目录或日志。外部服务自己的有效期仍有效，不因本地会话保存时间更长而延长。

会话默认按插件隔离，不自动按业务用户隔离。多用户业务必须选择不同的键，并在插件业务逻辑中校验使用者。不要把未经验证的查询参数直接作为其他用户的会话键。

### 11.5 可编译的示例

下面演示缓存会话与外部请求，并只向业务调用者返回成功状态。编译命令与第 7 节相同；使用本节的清单，安装后分别授权所需能力。

```go
package main

import (
 "encoding/base64"
 "encoding/json"
 "unsafe"
)
var buffers [][]byte
//go:wasmimport api_manager host_call
func hostCall(requestPtr,requestLen,responsePtr,responseCap uint32)uint64
func callHost(request any)map[string]any{
 input,_:=json.Marshal(request)
 output:=make([]byte,2<<20)
 result:=hostCall(uint32(uintptr(unsafe.Pointer(&input[0]))),uint32(len(input)),uint32(uintptr(unsafe.Pointer(&output[0]))),uint32(len(output)))
 if uint32(result)!=0{return map[string]any{"ok":false,"error":"host buffer failure"}}
 var response map[string]any
 if json.Unmarshal(output[:uint32(result>>32)],&response)!=nil{return map[string]any{"ok":false,"error":"invalid host response"}}
 return response
}
//go:wasmexport alloc
func alloc(size uint32)uint32{b:=make([]byte,size);buffers=append(buffers,b);return uint32(uintptr(unsafe.Pointer(&b[0])))}
//go:wasmexport handle
func handle(ptr,size uint32)uint64{
 // Cache-only and durable modes are explicitly selected per operation.
 // For update, read the stored version first and pass it back as version.
 current:=callHost(map[string]any{"operation":"session_get","session":map[string]any{"key":"demo.session","persist":false}})
 version:=float64(0)
 if value,ok:=current["result"].(map[string]any);ok{if v,ok:=value["version"].(float64);ok{version=v}}
 saved:=callHost(map[string]any{"operation":"session_put","session":map[string]any{"key":"demo.session","persist":false,"version":version,"ttl_seconds":60,"value":map[string]any{"message":"session-ready"}}})
 fetched:=callHost(map[string]any{"operation":"session_get","session":map[string]any{"key":"demo.session","persist":false}})
 external:=callHost(map[string]any{"operation":"http_request","http":map[string]any{"url":"https://example.com/","method":"GET"}})
 // The external response/cookie/token stays inside the plugin. Never expose
 // such secrets in your business API response merely to verify connectivity.
 status:=map[string]any{"storage_ok":saved["ok"],"session_found":fetched["ok"],"network_ok":external["ok"]}
 body,_:=json.Marshal(status)
 response,_:=json.Marshal(map[string]any{"status":200,"headers":map[string][]string{"Content-Type":{"application/json"},"Cache-Control":{"no-store"}},"body_base64":base64.StdEncoding.EncodeToString(body)})
 output:=alloc(uint32(len(response)));copy(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(output))),len(response)),response)
 return uint64(output)<<32|uint64(len(response))
}
func main(){}
```

此示例的外部目标为 `https://example.com`，请在后台明确授权该来源。网络能力关闭时会收到拒绝，不会绕过权限。如果只演示缓存，单独声明和授权 `session_storage` 即可。

### 11.6 保持关闭的边界

宿主文件系统、环境变量、Docker Socket、任意 SQL、直接 PostgreSQL/Redis 连接和裸 socket 仍不开放。`database_write` 仍是预留声明：当前没有为 WASM 注册任意数据库写入 host 函数。会话持久化只提供上述按插件隔离、加密和限额的接口。

## 12. 插件版本与升级

- 插件安装记录由主程序生成 ID，文件按 `PLUGIN_DIR/插件名/版本` 存储。同一名称和版本不要反复覆盖，应使用新的版本号。
- 主程序保存上传时的文件信息与 SHA-256，启用和重启加载时检查文件是否被改动。不要直接修改已安装目录中的清单或 WASM。
- 注册表按逻辑 `name` 选择当前运行模块，不会根据接口配置同时路由到该名称的多个版本。
- 建议先停用旧版本，再配置并启用新版本，避免版本开关与当前运行模块选择不一致；跨版本设置需要单独填写或核对。
- 在途调用使用已取得的模块租约；模块替换会等待当前调用结束后关闭旧运行时。
- 删除插件前必须停用。关联接口不会自动删除，需先下线或改绑，避免发布接口指向不可用插件。
- 容器重启会重新加载启用插件；文件损坏、必填设置不完整、声明未允许能力等问题会导致加载失败并停用该记录。
- 插件文件与数据库记录应一同备份。主程序数据库快照不包含插件二进制文件。

## 13. 错误排查

| 现象 / 错误 | 检查方向 |
| --- | --- |
| `manifest file is required` / `wasm file is required` | 上传分别使用 `manifest`、`wasm` 文件字段，不是 ZIP 包。 |
| `plugin entrypoint must be a local .wasm filename` | `entrypoint` 不能包含目录、URL 或父路径。 |
| `plugin name and version …` | 名称、版本的字符或长度不符合清单要求。 |
| `wasm plugin must export memory` | 模块是否导出了名为 `memory` 的内存。 |
| `wasm plugin must export alloc and handle functions` | 是否按 ABI 导出，而不是只提供 `main` / `_start`。 |
| `exports do not match the required ABI` | `alloc` 是 i32 → i32，`handle` 是两个 i32 → i64。 |
| `instantiate wasm module` / 缺少导入 | 检查是否依赖未提供的宿主模块；Go 示例按 wasip1 reactor 构建。 |
| `插件设置尚未填写完整` | 核对必填设置、默认值、数据类型和当前版本的配置。 |
| `plugin requests database_write … disabled` | 移除不需要的预留能力，不要把它当作已开放的 SQL 功能。 |
| `wasm memory write failed` / `response outside memory bounds` | 真实分配是否足够，响应缓冲区是否在返回后仍有效。 |
| `decode wasm response` | 返回的是完整响应封装 JSON，指针与长度打包顺序正确。 |
| `decode wasm response body` | `body_base64` 必须为标准 Base64。 |
| 超时、内存增长失败或运行时陷阱 | 检查初始化、死循环、分配量与清单限制，不应直接把限制提高到最大。 |
| `plugin binary checksum changed` / 清单与安装记录不一致 | 已安装文件被改动；重新提交新版本，勿修改运行目录。 |
| 接口已发布但插件不可用 | 分别检查安装、启用、接口 `plugin` 逻辑名称和接口发布状态。 |
| 预期有缓存但每次 MISS | 检查身份/请求差异、禁用缓存的头、响应状态/大小/Schema、TTL 和设置版本。 |

先在独立环境复现，并使用不含秘密的请求 ID 与安全错误摘要排查。不要为了调试打印全部请求头、设置对象或生产响应正文。

## 14. 发布检查清单

- [ ] 清单与 WASM 文件完整，名称和版本符合要求，记录了校验值。
- [ ] 导出 `memory`、`alloc`、`handle`，已通过主程序实际加载和调用。
- [ ] 有效请求、多值参数、空正文、无效输入及大输入均已验证。
- [ ] 返回正确的响应封装，正文 Base64 可解码，状态、头与响应 Schema 合法。
- [ ] 在实际 `timeout_ms` 和 `memory_mb` 限制下验证，不依赖跨请求内存。
- [ ] 必填设置有值，秘密使用 `writeOnly`，清单和源码无实际凭证。
- [ ] 不把 Cookie、KEY、设置秘密、堆栈或内部 SQL 写入响应和日志。
- [ ] 已验证 KEY / 无验证两种接口配置中真正需要支持的一种或两种。
- [ ] 缓存只用于可安全复用的业务结果，未把服务端缓存当作 CDN 缓存许可。
- [ ] 新版本设置、旧版本回退以及重启后加载均已核对。
- [ ] 先完成独立测试环境验证，再由有权限的管理员发布。

## 15. 对照实现

如实际实现与未来版本文档不同，应先核对当前代码与测试：

| 文件 | 负责内容 |
| --- | --- |
| `internal/plugin/wasm.go` | 清单、WASM ABI、实例生命周期、请求和响应限制。 |
| `internal/plugin/manager.go` | 上传、安装记录、启用/停用、存储路径和文件验证。 |
| `internal/plugin/library.go` | 本地插件库、版本目录和校验侧车。 |
| `internal/plugin/settings.go` | 设置校验、加密、脱敏、版本检查和运行时设置。 |
| `internal/plugin/database.go` | 尚未对 WASM 开放的宿主数据库写入边界。 |
| `internal/api/plugin_settings.go` | 后台设置 HTTP 接口。 |
| `internal/gateway/plugin_cache.go` | 响应缓存条件、缓存键、加密存储与命中逻辑。 |
| `internal/model/plugin_cache.go` | 缓存配置与大小限制。 |

Go 工具链的 WebAssembly reactor 与 `go:wasmexport` 支持可参见 [Go 官方发布说明](https://go.dev/doc/go1.24#wasm)。该链接说明编译器能力，不代表本项目已开放 WASI 的网络、文件或数据库能力。


## 15. 严格请求参数与插件更新

新上传、安装和更新的包必须声明至少一个 `routes` 项。每个接口均须提供 `parameters_schema` 和 `request_schema`；无参数也不能省略。参数结构只包含显式的 `path`、`query`、`header` 三个对象，路径与查询必须拒绝未知字段；每个参数必须说明类型和用途。路径模板中的占位符必须全部声明，不能多声明不存在的路径参数。无正文的接口使用 `request_schema: {type: 'null'}`。不允许 `$ref`、`$dynamicRef`、外部结构加载或过深的结构。

通过插件创建接口时，主程序根据插件名称、路径和请求方式选择声明，自动保存参数校验与说明，后台补填值不能替换声明。同一路径多种方法的参数结构必须一致，避免拆成多个含糊接口。公开文档和在线测试读取这些已保存的插件结构。

在“已安装插件 → 更新”上传同名的新版本清单和 WASM。插件编号、设置、能力授权、持久化会话及计费配置保留；已绑定接口同步新参数说明。新版本移除已绑定路由、缺少参数声明、结构不兼容或无法编译时会拒绝更新，旧运行版本继续工作。旧模块在正在执行的调用完成后释放，不会为更新而中断其他接口。

旧插件没有参数声明时不会被静默补上假说明；新安装或重新启用会被拒绝。请先按本规范发布带完整声明的新版本。更新后保留旧包目录用于人工回退与审计，不覆盖历史包。
