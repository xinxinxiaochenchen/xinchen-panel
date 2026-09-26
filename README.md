# Network Control Plane

独立设计的代理网络控制平面，按[架构设计](docs/superpowers/specs/2026-09-25-network-control-plane-design.md)分阶段实现。当前代码包含控制面基础、PostgreSQL 迁移、浏览器登录与 RBAC、用户和套餐管理、节点与单跳线路、直达转发、代理连接、订阅与分流、用量账本，以及登录后的资源操作页。纯 IP 预览继续关闭浏览器认证和 Agent TLS；真实节点数据面仍需在安全域名和 Agent 入网后验收。

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

前端位于 `apps/web`。运行 `npm --prefix apps/web ci` 与 `npm --prefix apps/web run build` 后，可设置绝对路径 `CONTROL_WEB_DIR=/path/to/apps/web/dist`，由 Go 服务在 `/` 提供静态页面，`/api/*` 仍由原 API 处理。开发时可在 `apps/web` 运行 `npm run dev`，Vite 会把 `/api` 代理到本机 8080。纯 HTTP 预览仅显示真实就绪状态和模块结构，不展示账户数据或开放登录。

## 数据库

`migrations/000001_init.up.sql` 定义首批身份、资源组、节点、线路、套餐、Agent 与 outbox 表；`000002_identity.up.sql` 增加角色、权限和浏览器会话；`000003_catalog_audit.up.sql` 增加目录操作审计及代理端点唯一索引；`000004_forward_rules.up.sql` 增加转发规则和端口占用表；`000005_config_revisions.up.sql` 增加按 Agent 节点保存的期望配置版本；`000006_agent_enrollment.up.sql` 增加一次性入网令牌和证书到期字段；`000007_agent_presence.up.sql` 增加 Agent 最新心跳指标。运行 `go run ./cmd/migrate up` 会按版本顺序在事务中应用 up migration，并校验已应用文件的 SHA-256；文件改动或补插旧版本会报错。down SQL 保留供人工回滚评审，命令不会自动执行降级。版本 7 已在独立 PostgreSQL 16 测试库验证，并于 2026-09-26 随提交 `231744f` 应用于纯 IP 预览的正式库。

## 资源目录开发状态

管理员可通过 `/api/v1/admin/resource-groups` 和 `/api/v1/admin/nodes` 创建及分页查询资源组、节点。普通用户通过 `/api/v1/nodes` 和 `/api/v1/nodes/{id}` 只能读取有效套餐快照授权的已启用节点。写请求要求 `nodes.write` 权限及 CSRF 令牌，创建操作与审计记录在同一事务中。列表参数 `limit` 和 `cursor` 见 [OpenAPI](api/openapi/control-plane.yaml)。这些路由随浏览器身份认证开关一起启用；公网纯 HTTP 预览上仍关闭。

## 套餐与订购开发状态

管理员可通过 `/api/v1/admin/plans` 创建带明确资源组、共享单跳线路授权与额度限制的套餐，并通过 `/api/v1/admin/memberships` 为已有用户创建立即生效的订购。订购事务把额度、倍率、限制和授权 ID 冻结在快照中；同一用户不能同时拥有两个有效订购。`/api/v1/my/membership` 和 `/api/v1/my/entitlements` 只读取登录用户自己的有效订购。套餐授权共享线路时要求单跳出口节点属于授权资源组；私有线路不得授予其他用户。创建订购要求结束时间至少比数据库当前时间晚五分钟。接口契约见 [OpenAPI](api/openapi/control-plane.yaml)。

## 单跳线路开发状态

管理员可通过 `GET/POST /api/v1/admin/lines` 与 `GET/PATCH /api/v1/admin/lines/{id}` 管理共享单跳线路。普通用户可通过 `GET/POST /api/v1/lines`、`GET/PATCH/DELETE /api/v1/lines/{id}` 管理套餐允许的自有线路并查看获授权的共享线路。线路与节点独立建模，首版只接受一个代理出口 hop；自有线路数受订购快照约束。管理列表保留停用的自有线路；实际连接使用应调用 `GetUsableLine` 复核启用、节点能力和当前授权。公网纯 HTTP 预览保持关闭这些路由。

## 直达转发规则开发状态

普通用户可通过 `GET/POST /api/v1/forward-rules` 和 `GET/PATCH/DELETE /api/v1/forward-rules/{id}` 创建、查看、改名、启停及删除自有的直达转发规则。入口节点必须具备 `forward` 能力且属于有效订购授权的资源组；目标可为授权节点或公网地址。管理员通过 `/api/v1/admin/forward-target-policies` 批准目标类型、节点组、协议和目标端口范围；默认无授权，普通用户不能自行放开。TCP、UDP 与 BOTH 分别原子占用对应端口，每节点规则数受订购快照约束；停用保留端口，删除释放端口。多跳线路尚未开放，端口与目标变更需删除后重建。写入审计和 outbox 后，状态仍为待下发；Agent 执行链路未接入前不会有实际转发。公网纯 HTTP 预览保持关闭这些路由。

## Trojan 代理连接开发状态

开发分支新增 `proxy_accesses` 迁移 8、每连接随机凭据、Trojan SHA-224 摘要和 AES-256-GCM 加密存储。创建与启用时复核当前有效订购、单跳线路、出口节点和资源组授权；用户只能查询和管理自己的连接，凭据只经专门的授权响应返回。创建、轮换、更新和删除写审计及待收敛事件。启用浏览器身份认证前必须提供权限为 `0600` 的绝对路径 `CONTROL_PROXY_CREDENTIAL_KEY_FILE`，文件内容为 32 字节密钥的无填充 base64url 编码。缺少密钥时启动会拒绝开启浏览器身份路由。迁移 8 和仓储生命周期已在独立 PostgreSQL 16 测试库验证；正式库当前仍为迁移 7，纯 IP 预览未包含此分支改动。控制面能按节点和授权生成哈希凭据配置，经 mTLS 下发给 Agent；Agent 使用单独的 TLS 服务端证书执行 Trojan TCP CONNECT，仅允许公网目标，凭据轮换和停用可断开旧连接。订阅、用量计费和额度租约仍待实现，因此暂不在公网部署真实代理。

Agent 的代理证书使用 `CONTROL_AGENT_PROXY_CERT_FILE` 与 `CONTROL_AGENT_PROXY_KEY_FILE` 指定，必须是绝对路径，私钥权限不超过 `0600`。Agent 收到代理配置但没有 TLS 服务端证书时会 NACK 并保留旧配置。控制面每分钟续期五分钟的执行配置租约；断线或租约到期时 Agent 关闭监听，套餐到期也会关闭相应连接。该租约不代表流量额度控制。

## Agent 转发运行时开发状态

`internal/agentruntime` 实现独立的 TCP/UDP 直达转发执行器。调用方传入带递增版本号的完整规则快照；运行时先验证目标和端口、预绑定所有新增监听，失败时保留上一个版本。TCP 与 UDP 在连接时解析目标并拒绝私网、回环和保留地址；TCP 连接数、UDP 待处理包与客户端关联数均有上限。停用规则或更换目标会撤销旧 TCP 连接及 UDP 关联；UDP 关联按客户端活动时间过期。运行时支持注入 DNS/拨号器以进行真实套接字测试。Agent 进程现已接入此运行时与 mTLS 配置流，但线上 `us dmit` 尚未配置 Agent TLS、部署 Agent 或执行真实转发。当前纯 IP HTTP 预览提供只读页面与健康检查。

`internal/orchestration` 可从 PostgreSQL 的一致性只读快照中编译单节点转发配置。编译时重新检查账户、有效订购快照、资源组、节点能力和目标策略，剔除失效规则；非法目标、损坏的订购快照或监听冲突会被单独排除并返回诊断，避免阻断其他规则的撤销。节点停用时直接生成空配置。输出规则顺序固定。每节点规则数是创建上限；正数上限下调不自动删减已有规则，降为零则撤销该套餐的转发能力。

配置版本仓储在锁定 Agent 后，于同一数据库事务读取当前节点事实并编译完整转发快照。可执行内容的 SHA-256 不变时不新增版本，但会刷新逐规则诊断；变化时递增 `desired_revision`。Agent 回执必须匹配节点、版本和摘要；应用成功才推进 `applied_revision`，失败回执可随重试更新，但已应用版本不会被失败回执覆盖。失败原因有长度和字符限制。收敛 worker 消费转发规则与目标策略的 outbox 事件，并定期扫描 Agent 节点，以处理套餐到期等时间驱动的撤销。Agent 身份、配置流和回执传输已有实现；线上 Agent TLS 与 Agent 进程尚未启用，故服务器当前不会执行转发。

`internal/agentproto` 定义版本 1 的 JSON 消息封包和直达转发配置/结果负载。封包限制为 1 MiB，拒绝未知版本、字段、重复 JSON 字段和无效身份；Agent 验证快照有效期及与控制面一致的可执行内容摘要后才能应用。协议已连接到独立 mTLS WebSocket 监听器和 Agent 进程。

`internal/agentidentity` 增加 Agent 入网身份基础：10 分钟一次性令牌只以 SHA-256 存库；Agent 以 Ed25519 CSR 换取绑定节点 URI 的 24 小时客户端证书；签发和令牌消费在同一事务中完成。证书认证会核对 CA、节点 URI、当前数据库指纹、节点启用状态与吊销状态。启用独立 TLS 监听器时，管理员须用当前密码重新认证，并经 `POST /api/v1/admin/nodes/{id}/agent-enrollment` 签发令牌；签发记录审计但不保存明文令牌。Agent 经 TLS `POST /api/v1/agent/enroll` 入网，入口有单进程 IP、令牌及全局速率限制。TLS 监听器需要 `CONTROL_AGENT_TLS_ADDR`、`CONTROL_AGENT_TLS_CERT_FILE`、`CONTROL_AGENT_TLS_KEY_FILE`、`CONTROL_AGENT_CA_CERT_FILE`、`CONTROL_AGENT_CA_KEY_FILE` 全部配置；监听地址目前限回环，私钥文件权限不能对组或其他用户开放。CA 私钥不写入数据库或响应，Agent 私钥不离开节点。当前公网纯 IP 预览不启用这些配置，故没有入网或令牌路由。

`cmd/agent` 提供独立 Agent 进程和 `enroll` 命令。入网命令从标准输入读取一次性令牌，在 Agent 本机生成 Ed25519 私钥，经受信任 HTTPS 换取证书，并仅新建权限为 `0600` 的证书和私钥文件。运行进程主动建立 mTLS WebSocket，发送 hello 与 15 秒心跳，接收完整转发快照，由 `agentruntime` 原子应用并回传 ACK/NACK；断线会关闭转发监听并重连。心跳采集 Linux `/proc` 的 CPU、内存、网卡字节及本机活动转发连接数。控制面数据库保存最新指标，45 秒无心跳会转为离线。迁移 `000007_agent_presence` 增加指标表。`cmd/agent-token` 可在受限服务器本机为现有管理员和节点签发令牌到新建的私有文件；命令不在标准输出打印令牌。证书轮换、控制面公网 Agent TLS 接入、多节点部署和代理连接尚未完成，当前纯 IP HTTP 预览继续关闭 Agent TLS 与浏览器登录。

## 用户生命周期开发状态

管理员可通过 `POST /api/v1/admin/users` 创建普通用户，通过 `GET /api/v1/admin/users` 分页查看用户。创建请求的初始密码仅用于生成 bcrypt 哈希，不进入响应或审计记录。用户通过 `POST /api/v1/me/password` 提交旧密码和新密码，成功后所有浏览器会话在同一数据库事务中撤销，当前 Cookie 也会清除。登录会话创建与密码轮换使用用户行锁避免旧密码并发登录；改密尝试每账户限 5 次/5 分钟。创建与改密接口需要 HTTPS、有效会话与 CSRF 令牌；当前公网纯 HTTP 预览保持关闭。

## 身份认证开发状态

代码提供 `POST /api/v1/auth/login`、`POST /api/v1/auth/logout`、`GET /api/v1/me` 和 `GET /api/v1/me/permissions`。会话使用 12 小时有效的随机令牌，数据库只存哈希；浏览器 Cookie 带 `Secure`、`HttpOnly`（会话）和 `SameSite=Lax`，写请求使用 CSRF 令牌。登录在单进程内限制为每账户 5 次/5 分钟、全局 60 次/分钟；过期会话每小时分批清理。首次管理员由 `cmd/admin-bootstrap` 创建，邮箱通过 `CONTROL_ADMIN_EMAIL` 提供，密码从非交互标准输入读取且不得少于 12 字节。完整契约见 [OpenAPI](api/openapi/control-plane.yaml)。

浏览器身份路由默认关闭，需显式设置 `CONTROL_BROWSER_AUTH_ENABLED=true`。当前公网纯 HTTP 预览已部署身份数据表，但浏览器身份路由关闭，登录请求返回 404。需要先准备 HTTPS 反代、将主机绑定恢复为回环地址，并补齐登录审计，再开启身份功能。多实例部署前应把进程内登录限流改为 Redis 共享限流。

## 后续阶段

按模块继续增加：用户状态与角色管理、套餐编辑与账期、Agent 证书轮换与实际节点部署、代理连接、订阅与分流、流量计费以及可操作的前端控制台。当前 Docker Compose 部署只是纯 IP 只读预览。用户确认的 MVP 采用单跳线路、Trojan over TLS 和上传加下载的流量口径。

当前基础服务的纯 IP 只读预览部署见[部署说明](docs/deployment/private-preview.md)。预览实例可检查页面与服务状态，不代表完整控制台已经上线。

## 订阅（已纳入只读预览镜像，公网入口关闭）

用户通过 `/api/v1/subscriptions` 创建绑定已有代理连接的订阅；支持多订阅、名称模板、启停、删除及 Token 重置。套餐 `max_subscriptions` 包含停用订阅。Token 使用随机 256 位值，数据库只保存 SHA-256 哈希与带订阅/所有者上下文的 AES-GCM 密文；元数据和审计不包含 Token。

`GET /api/v1/subscriptions/{id}/url?format=mihomo|sing-box|surge` 返回 origin-relative `path`，前端用当前配置的 HTTPS 地址组合显示。`preview` 输出相同配置。`GET /sub/{token}/{format}` 每次检查有效套餐、资源授权、启停、45 秒 Agent 在线窗口和已 ACK 的凭据摘要；没有可用连接返回 503，不生成直连兜底。控制面日志会脱敏订阅路径，后续 Nginx 的访问日志也需配置相同脱敏规则。公开订阅按 Token 和全局限流；部署 HTTPS 反代时，应在可信 Nginx 层设置按真实客户端 IP 的限流，控制面不信任外部 Forwarded 头。

当前提供 Mihomo YAML、sing-box 1.12+ JSON 与 Surge 文本配置，均只生成 Trojan TCP、证书校验及代理选择组。Mihomo/sing-box 使用 127.0.0.1:7890 混合入口，Surge 使用本地 HTTP 6152 与 SOCKS5 6153 入口。名称模板支持 `{name}`、`{line}`、`{region}`、`{index}`；重复名称自动区分。订阅页提供格式切换、地址复制、Token 重置、启停及预览。格式结构测试已运行，原生客户端二进制兼容性尚未验证。Clash 单独兼容格式和 GeoSite 受控规则集仍待实现。纯 IP 预览保持订阅和浏览器认证入口关闭。
