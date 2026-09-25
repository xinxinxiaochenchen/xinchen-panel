# Network Control Plane

独立设计的代理网络控制平面，按[架构设计](docs/superpowers/specs/2026-09-25-network-control-plane-design.md)分阶段实现。本仓库当前实现的是第一阶段控制面基础，尚未开放用户、节点、线路、转发、订阅、分流、计费和 Agent 业务 API。

## 本地运行

需要 Go 1.27.1。执行：

```sh
go test ./...
go vet ./...
go run ./cmd/control-plane
```

默认监听 `127.0.0.1:8080`。可设置 `CONTROL_HTTP_ADDR`（例如 `127.0.0.1:9090`）和 `CONTROL_LOG_LEVEL`（`debug`、`info`、`warn`、`error`）。探针：

```sh
curl http://127.0.0.1:8080/api/v1/health/live
curl http://127.0.0.1:8080/api/v1/health/ready
```

当前 `/ready` 仅表示 HTTP 服务可以处理请求。接入 PostgreSQL 后会加入数据库探测。响应带 `X-Request-ID`，错误使用统一 JSON envelope。接口契约位于 `api/openapi/control-plane.yaml`。

## 数据库

`migrations/000001_init.up.sql` 和 `000001_init.down.sql` 定义首批身份、资源组、节点、线路、套餐、Agent 与 outbox 表。此阶段尚未集成 migration 执行器，也未在真实 PostgreSQL 上运行；不要将它作为生产迁移使用。下一阶段将加入受控迁移执行、真实数据库集成测试和连接就绪探测。

## 后续阶段

按模块依次增加：身份与 RBAC、节点/线路目录、Agent 同步、代理连接与转发、订阅与分流、流量计费、前端控制台以及 Docker Compose 部署。用户确认的 MVP 采用单跳线路、Trojan over TLS 和上传加下载的流量口径。
