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

初始化脚本生成 `secrets/` 目录及六个相互独立的凭据，不会显示或覆盖已有值。应用使用只读文件挂载读取凭据，不需要手工填写数据库地址、DSN 或加密密钥。

访问 `http://127.0.0.1:8080/console/`，输入 `secrets/admin_token` 中的**管理员 KEY**即可登录，无需创建账号或初始化管理员：

```bash
cat secrets/admin_token
```

请仅在可信终端读取 KEY，不要把 KEY 放入 URL、工单、聊天记录或源代码。

## KEY 认证

API Manager 仅使用 KEY 认证。

| KEY | 用途 | 来源 |
| --- | --- | --- |
| 管理员 KEY | 控制台及管理 API，拥有全部管理权限 | `secrets/admin_token` |
| 调用 KEY | 已发布的业务 API；可设置有效期、吊销或轮换 | 控制台“调用凭证” |
| 指标 KEY | `/metrics` 抓取；不能访问管理 API | `secrets/metrics_token` |

业务请求使用：

```bash
curl -H 'X-API-Key: <调用 KEY>' http://127.0.0.1:8080/api/example
```

也支持 `Authorization: Bearer <KEY>` 作为 KEY 的传输格式。不要同时提供两个认证头。所有业务接口默认要求 KEY，不能设置免认证或其他认证模式。

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
| `API_MANAGER_IMAGE` | `docker.io/dingding229/api-manager:latest` | Docker Hub 镜像标签 |
| `SECRETS_DIR` | `./secrets` | 只读凭据文件目录 |
| `API_BIND_ADDR` | `127.0.0.1` | API 与 Grafana 的宿主机绑定地址 |
| `API_PORT` | `8080` | 管理控制台和业务 API 端口 |
| `GRAFANA_PORT` | `3000` | Grafana 端口 |
| `OBSERVABILITY_STACK_ENABLED` | `false` | 启动完整观测栈 |
| `API_MEMORY_LIMIT` | `2g` | API Manager 容器内存上限 |
| `API_CPUS` | `2.0` | API Manager CPU 上限 |

修改配置后执行 `docker compose up -d`。

### 观测

API Manager 内置日志、指标、Trace、告警和 Dashboard。镜像还包含 Loki、Alloy、Tempo、Prometheus、Alertmanager、Grafana。

启用六组件观测栈：

```bash
OBSERVABILITY_STACK_ENABLED=true docker compose up -d api-manager
```

Grafana 地址：`http://127.0.0.1:3000/`，用户名 `admin`，密码在 `secrets/grafana_admin_password`。完整栈建议宿主机至少提供 4 GiB 内存，以同时运行 API、数据库和观测组件。

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
- 公网访问必须使用 HTTPS 反向代理，不要直接暴露明文管理端口。管理员 KEY 不应交给业务调用方。
- 控制台 KEY 只保存在当前浏览器会话中，退出会清除缓存；轮换管理员 KEY 后需重新创建 API 容器。
- 更新旧部署前先备份，保留原管理员 KEY、凭据加密密钥及 PostgreSQL 密码。外部数据库中的数据需要先迁移到新 PostgreSQL，不能仅启动一个空数据库替代。
- 已有接口会转换为 KEY 保护；旧认证快照不能直接回滚为其他模式。旧插件清单中的认证声明需更新为 `api_key`。
- 单实例及同容器观测栈不提供组件级隔离或高可用。上线前完成容量测试和备份恢复验证。

## Kubernetes

`deploy/helm/api-manager` 为可选 Helm Chart，同样使用 Docker Hub `latest` 和 KEY 认证。Kubernetes 模式需要现有 PostgreSQL、Redis、Secret、持久卷及明确的 NetworkPolicy 放行规则；外部数据库默认要求 TLS。常规部署优先使用上面的单文件 Compose。
