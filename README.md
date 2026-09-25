# Network Control Plane

独立设计的代理网络控制平面，按[架构设计](docs/superpowers/specs/2026-09-25-network-control-plane-design.md)分阶段实现。本仓库当前实现的是第一阶段控制面基础，尚未开放用户、节点、线路、转发、订阅、分流、计费和 Agent 业务 API。

## 本地运行

需要 Go 1.27.1 和 PostgreSQL。设置 `CONTROL_DATABASE_URL`，例如 `postgres://app:secret@127.0.0.1:5432/control`，再执行：

```sh
go test ./...
go vet ./...
go run ./cmd/migrate up
go run ./cmd/control-plane
```

默认监听 `127.0.0.1:8080`。可设置 `CONTROL_HTTP_ADDR`（例如 `127.0.0.1:9090`）和 `CONTROL_LOG_LEVEL`（`debug`、`info`、`warn`、`error`）。探针：

```sh
curl http://127.0.0.1:8080/api/v1/health/live
curl http://127.0.0.1:8080/api/v1/health/ready
```

`/live` 只检查进程；`/ready` 在数据库不可用时返回 503。响应带 `X-Request-ID`，错误使用统一 JSON envelope。接口契约位于 `api/openapi/control-plane.yaml`。

## 数据库

`migrations/000001_init.up.sql` 和 `000001_init.down.sql` 定义首批身份、资源组、节点、线路、套餐、Agent 与 outbox 表。运行 `go run ./cmd/migrate up` 会按版本顺序在事务中应用 up migration，并校验已应用文件的 SHA-256；文件改动或补插旧版本会报错。down SQL 保留供人工回滚评审，命令不会自动执行降级。两份 SQL 已通过 PostgreSQL 语法解析，并在临时 PGlite 实例中完成建表及回滚检查；Go migration 命令尚未在部署用 PostgreSQL 16 上验证，生产使用前需完成此项测试。

## 后续阶段

按模块依次增加：身份与 RBAC、节点/线路目录、Agent 同步、代理连接与转发、订阅与分流、流量计费、前端控制台以及 Docker Compose 部署。用户确认的 MVP 采用单跳线路、Trojan over TLS 和上传加下载的流量口径。
