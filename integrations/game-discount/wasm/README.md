# Game Discount WASM 上传验证包

此目录的 `manifest.yaml` + `plugin.wasm` 可在 API Manager 控制台“插件”页上传（两个文件分别选择）。上传后**默认禁用**，需在列表中点击“启用”；单独上传插件并不会创建 API 路由。 manifest 的 `routes` 已声明下面三条测试路由，控制台会据此提供建议和示例；**此前已上传同名同版本的插件不会自动更新其数据库中的 manifest**，需提高版本号再重新上传并启用。可以先在“接口管理”新增以下三条独立测试路由，**不要修改现有 `/api/game-discount` 代理接口**：

| 方法 | 路径 | 插件名称 | 鉴权 |
| --- | --- | --- | --- |
| GET | `/api/game-discount-wasm/v1/status` | `game-discount-wasm` | API Key |
| GET | `/api/game-discount-wasm/v1/offers` | `game-discount-wasm` | API Key |
| GET | `/api/game-discount-wasm/v1/offers/{external_id}` | `game-discount-wasm` | API Key |

创建并发布后，用已有调用凭证测试（请求示例见下方）。不能同时在这三条测试 API 上填写上游 URL；否则网关会优先走上游代理。测试完成后，可下线测试路由并在“插件”页禁用插件。

```bash
KEY='你的 API Key' # 不要将真实密钥写入仓库或公开日志
curl -H "X-API-Key: $KEY" 'http://localhost:8080/api/game-discount-wasm/v1/status'
curl -H "X-API-Key: $KEY" 'http://localhost:8080/api/game-discount-wasm/v1/offers?region=HK'
curl -H "X-API-Key: $KEY" 'http://localhost:8080/api/game-discount-wasm/v1/offers/wasm-demo-001'
```

源码位于相邻项目 `../game-discount-api/cmd/wasm-plugin/main.go`，依赖原项目的 `internal/models` 响应类型。修改源码后重新构建：

```bash
./scripts/build-game-discount-wasm.sh
```

要求本机 Go 支持 `GOOS=wasip1` 与 `//go:wasmexport`（本地用 Go 1.26 验证）。生成文件约 5 MiB，低于控制台默认 20 MiB 上传限制。版本号修改时同步更新 `manifest.yaml`。

**能力边界：**这是用于上传与 ABI/运行验证的 *demo* 插件，不是原项目 PostgreSQL 服务的完整迁移。它使用原项目的响应模型，仅提供一条明确标记 `wasm-demo-001` 的示例报价和状态。WASM 沙箱不提供数据库、网络、环境变量或宿主文件系统访问；不能查询现有游戏服务数据库或实时价格，不能承接真实业务流量。原有生产上游代理保持不变。业务鉴权、限流、审计仍由 API Manager 网关负责。
