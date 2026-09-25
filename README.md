# Network Control Plane

独立设计的代理网络控制平面，按[架构设计](docs/superpowers/specs/2026-09-25-network-control-plane-design.md)分阶段实现。当前代码包含控制面基础、PostgreSQL 迁移、浏览器登录与 RBAC、用户创建和密码轮换、资源组和节点目录、单跳线路，以及套餐和订购授权 API。转发、订阅、分流、计费和 Agent 业务 API 尚未实现。

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

`migrations/000001_init.up.sql` 定义首批身份、资源组、节点、线路、套餐、Agent 与 outbox 表；`000002_identity.up.sql` 增加角色、权限和浏览器会话；`000003_catalog_audit.up.sql` 增加目录操作审计及代理端点唯一索引。运行 `go run ./cmd/migrate up` 会按版本顺序在事务中应用 up migration，并校验已应用文件的 SHA-256；文件改动或补插旧版本会报错。down SQL 保留供人工回滚评审，命令不会自动执行降级。版本 3 已在独立 PostgreSQL 16 测试库验证应用和回滚。

## 资源目录开发状态

管理员可通过 `/api/v1/admin/resource-groups` 和 `/api/v1/admin/nodes` 创建及分页查询资源组、节点。普通用户通过 `/api/v1/nodes` 和 `/api/v1/nodes/{id}` 只能读取有效套餐快照授权的已启用节点。写请求要求 `nodes.write` 权限及 CSRF 令牌，创建操作与审计记录在同一事务中。列表参数 `limit` 和 `cursor` 见 [OpenAPI](api/openapi/control-plane.yaml)。这些路由随浏览器身份认证开关一起启用；公网纯 HTTP 预览上仍关闭。

## 套餐与订购开发状态

管理员可通过 `/api/v1/admin/plans` 创建带明确资源组、共享单跳线路授权与额度限制的套餐，并通过 `/api/v1/admin/memberships` 为已有用户创建立即生效的订购。订购事务把额度、倍率、限制和授权 ID 冻结在快照中；同一用户不能同时拥有两个有效订购。`/api/v1/my/membership` 和 `/api/v1/my/entitlements` 只读取登录用户自己的有效订购。套餐授权共享线路时要求单跳出口节点属于授权资源组；私有线路不得授予其他用户。创建订购要求结束时间至少比数据库当前时间晚五分钟。接口契约见 [OpenAPI](api/openapi/control-plane.yaml)。

## 单跳线路开发状态

管理员可通过 `GET/POST /api/v1/admin/lines` 与 `GET/PATCH /api/v1/admin/lines/{id}` 管理共享单跳线路。普通用户可通过 `GET/POST /api/v1/lines`、`GET/PATCH/DELETE /api/v1/lines/{id}` 管理套餐允许的自有线路并查看获授权的共享线路。线路与节点独立建模，首版只接受一个代理出口 hop；自有线路数受订购快照约束。管理列表保留停用的自有线路；实际连接使用应调用 `GetUsableLine` 复核启用、节点能力和当前授权。公网纯 HTTP 预览保持关闭这些路由。

## 用户生命周期开发状态

管理员可通过 `POST /api/v1/admin/users` 创建普通用户，通过 `GET /api/v1/admin/users` 分页查看用户。创建请求的初始密码仅用于生成 bcrypt 哈希，不进入响应或审计记录。用户通过 `POST /api/v1/me/password` 提交旧密码和新密码，成功后所有浏览器会话在同一数据库事务中撤销，当前 Cookie 也会清除。登录会话创建与密码轮换使用用户行锁避免旧密码并发登录；改密尝试每账户限 5 次/5 分钟。创建与改密接口需要 HTTPS、有效会话与 CSRF 令牌；当前公网纯 HTTP 预览保持关闭。

## 身份认证开发状态

代码提供 `POST /api/v1/auth/login`、`POST /api/v1/auth/logout`、`GET /api/v1/me` 和 `GET /api/v1/me/permissions`。会话使用 12 小时有效的随机令牌，数据库只存哈希；浏览器 Cookie 带 `Secure`、`HttpOnly`（会话）和 `SameSite=Lax`，写请求使用 CSRF 令牌。登录在单进程内限制为每账户 5 次/5 分钟、全局 60 次/分钟；过期会话每小时分批清理。首次管理员由 `cmd/admin-bootstrap` 创建，邮箱通过 `CONTROL_ADMIN_EMAIL` 提供，密码从非交互标准输入读取且不得少于 12 字节。完整契约见 [OpenAPI](api/openapi/control-plane.yaml)。

浏览器身份路由默认关闭，需显式设置 `CONTROL_BROWSER_AUTH_ENABLED=true`。当前公网纯 HTTP 预览已部署身份数据表，但浏览器身份路由关闭，登录请求返回 404。需要先准备 HTTPS 反代、将主机绑定恢复为回环地址，并补齐登录审计，再开启身份功能。多实例部署前应把进程内登录限流改为 Redis 共享限流。

## 后续阶段

按模块继续增加：用户状态与角色管理、套餐编辑与账期、Agent 同步、代理连接与转发、订阅与分流、流量计费以及前端控制台。当前 Docker Compose 部署只是纯 IP 技术预览。用户确认的 MVP 采用单跳线路、Trojan over TLS 和上传加下载的流量口径。

当前基础服务的纯 IP 预览部署见[部署说明](docs/deployment/private-preview.md)。预览实例仅用于健康检查，不代表完整控制台已经上线。
