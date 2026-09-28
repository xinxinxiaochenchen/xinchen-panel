# Network Control Plane

独立设计的代理网络控制平面，提供浏览器登录、RBAC、用户和套餐管理、节点与多跳线路、TCP/UDP 转发、Trojan 代理连接、订阅与分流、用量账本和审计。React 前端由 Go 控制面提供静态资源，PostgreSQL 保存数据，节点服务器运行独立 Go Agent。项目不包含原生 App、订单、充值或支付模块。

## 正式部署

管理面支持 **纯 IP HTTP** 和 **HTTPS 反代**，使用者自行选择。HTTP 模式提供真实登录和全部获授权管理功能；只读预览通过单独开关选择。Agent mTLS 与 Trojan TLS 是节点协议，与浏览器管理入口是否使用 HTTPS 分别配置。完整步骤见 [VPS WebUI 部署说明](docs/deployment/vps-webui.md)。

VPS 安装 curl、Git、Docker Engine 和 Compose 插件后，以 root 执行：

```sh
curl -fsSL https://raw.githubusercontent.com/xinxinxiaochenchen/xinchen-panel/main/scripts/install-vps.sh -o xinchen-panel-install.sh && sh xinchen-panel-install.sh --public-http
```

首次安装生成数据库密码和代理凭据密钥，提示输入管理员邮箱和密码，构建、迁移并启动面板。访问 `http://服务器IP:18080/` 即可登录管理；需在防火墙允许 TCP 18080。安装目录默认 `/opt/xinchen-panel`。安装器不要求 VPS 安装 Go 或 Node.js。

| 模式 | 参数 | 浏览器功能 |
| --- | --- | --- |
| IP HTTP | `--public-http` | 登录、个人功能、管理功能；非 Secure 的 host-only Cookie |
| HTTPS 反代 | `--https` | 同一组功能；回环绑定和 Secure Cookie，自行配置反代 |
| 只读预览 | `--public-preview` | 页面结构与健康状态，关闭登录和写操作 |

无参数首次安装默认正式 HTTP；无参数重复安装保留原访问模式、密码、密钥和数据卷，迁移前自动备份。显式参数可切换模式。已有源码运行 `sh scripts/deploy-vps.sh --public-http`；后续运行 `sh scripts/deploy-vps.sh` 更新。非交互安装使用 `CONTROL_ADMIN_EMAIL` 和权限 `0600` 的绝对路径 `CONTROL_ADMIN_PASSWORD_FILE`；管理员密码不写入 `.env` 或命令参数。

登录后可管理资源与账户。执行真实代理和转发前，需按部署文档接入节点 Agent，并配置节点、线路、套餐和转发目标策略。多跳还需持久化线路密钥。开发测试与生产构建在本地完成，服务器现场验收另行安排；本轮开发不改既有线上实例。`us bwg` 仍为历史只读预览，其状态以 [部署记录](docs/deployment/us-bwg-preview.md) 为准。

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

前端位于 `apps/web`。运行 `npm --prefix apps/web ci` 与 `npm --prefix apps/web run build` 后，可设置绝对路径 `CONTROL_WEB_DIR=/path/to/apps/web/dist`，由 Go 服务在 `/` 提供静态页面，`/api/*` 仍由原 API 处理。开发时可在 `apps/web` 运行 `npm run dev`，Vite 会把 `/api` 代理到本机 8080。前端根据 `/api/v1/me` 的响应显示登录、真实功能或只读预览，HTTP 与 HTTPS 均可使用。直接本地运行需启用 `CONTROL_BROWSER_AUTH_ENABLED=true`、设置代理凭据密钥，并在 HTTP 下设置 `CONTROL_BROWSER_COOKIE_SECURE=false`。

## 数据库

`migrations/000001_init.up.sql` 定义首批身份、资源组、节点、线路、套餐、Agent 与 outbox 表；后续迁移依次加入身份与审计、转发与配置版本、Agent 入网与指标、代理连接与订阅、账期计费、证书续签及受控 GeoSite/GeoIP 规则集。运行 `go run ./cmd/migrate up` 会按版本顺序在事务中应用 up migration，并校验已应用文件的 SHA-256；文件改动或补插旧版本会报错。down SQL 保留供人工回滚评审，命令不会自动执行降级。迁移 24/25 分别加入线路绑定转发和代理候选池；已在 `us bwg` 的 PostgreSQL 16.10 独立测试库验证，并应用到正式预览库。

## 资源目录开发状态

节点支持可选 `relay_port`，预留该节点 TCP/UDP 端口用于中继；须具备 `forward` 能力且与代理端口不同。握手契约见 [Agent 中继协议](docs/agent-relay-protocol.md)。多跳 TCP 的配置流和 Agent 执行器已接入，正式节点端到端验收仍待完成。

管理员可通过 `/api/v1/admin/resource-groups` 和 `/api/v1/admin/nodes` 创建及分页查询资源域、节点，并通过 `PATCH /api/v1/admin/resource-groups/{id}` 启停资源域。资源域状态变更会写审计并触发全局配置收敛。WebUI 可创建和编辑节点的公网 IP、带宽、计费倍率、标签、代理端口和中继端口；编辑使用 `PATCH /api/v1/admin/nodes/{id}/config`，节点启停仍使用独立状态接口。资料变更写审计并触发配置收敛，地址或中继参数变化会使现有中继证书授权失效，需要 Agent 重新申请。普通用户通过 `/api/v1/nodes` 和 `/api/v1/nodes/{id}` 只能读取有效套餐快照授权的已启用节点。写请求要求 `nodes.write` 权限及 CSRF 令牌，创建操作与审计记录在同一事务中。列表参数 `limit` 和 `cursor` 见 [OpenAPI](api/openapi/control-plane.yaml)。这些路由随浏览器身份认证开关一起启用；公网纯 HTTP 预览上仍关闭。

发布包中的 `node-bootstrap` 也支持用 `CONTROL_NODE_RELAY_PORT` 初始化中继端口；该端口会参与幂等校验并写入节点记录，必须与代理端口不同且节点具备 `forward` 能力。

## 套餐与订购开发状态

管理员可通过 `/api/v1/admin/plans` 创建、分页查看并归档/恢复带明确资源组、共享线路授权与额度限制的套餐，并通过 `/api/v1/admin/memberships` 为已有用户创建立即生效的订购及分页查看历史授权。订购事务把额度、倍率、限制和授权 ID 冻结在快照中；同一用户不能同时拥有两个有效订购。`/api/v1/my/membership` 和 `/api/v1/my/entitlements` 只读取登录用户自己的有效订购。套餐授权共享线路时要求整条线路的每个 hop 属于授权资源组，节点能力、端口和 `max_hops` 均满足套餐限制；私有线路不得授予其他用户。创建订购要求结束时间至少比数据库当前时间晚五分钟。管理员可取消现有授权；取消会保留历史账本和快照、阻止新的额度准入，并触发节点配置收敛。已发放的短期额度租约需到期或由 Agent 收到撤销配置后结束。接口契约见 [OpenAPI](api/openapi/control-plane.yaml)。

## 线路开发状态

管理员可通过 `GET/POST /api/v1/admin/lines` 与 `GET/PATCH /api/v1/admin/lines/{id}` 管理共享线路。普通用户可通过 `GET/POST /api/v1/lines`、`GET/PATCH/DELETE /api/v1/lines/{id}` 管理套餐允许的自有线路并查看获授权的共享线路。线路与节点独立建模。2–8 跳 TCP 线路可启用，入口须有 `proxy` 和 `forward` 能力及两个独立端口，后续节点须有 `forward` 能力和中继端口。每跳均检查资源组授权及套餐 `max_hops`。用户可在启用线路上创建代理连接；只有各跳 Agent 在线、证书有效、下游应用当前世代且入口应用代理配置后，订阅才会导出。计费准入沿整条线路复核这些条件，仅在入口建立一条用量会话并冻结线路与倍率；独立 PostgreSQL 集成验收已通过并发布到 `us bwg`。线路权重已用于订阅导出的稳定线路顺序；候选池的 Agent 拨号重试、最终线路额度请求和自动线路订阅目标已接入，多线路创建按套餐上限、线路授权和拓扑校验开放，PostgreSQL 16 的候选、计费和订阅闭环已逐项验证并发布到 `us bwg`。启用多跳数据面须配置 `CONTROL_RELAY_SECRET_KEY_FILE` 和 Agent mTLS；公网纯 HTTP 预览保持关闭管理路由。
线路详情可通过 `GET /api/v1/admin/lines/{id}/health` 或授权用户的 `GET /api/v1/lines/{id}/health` 查看。健康状态包含 `disabled`、`unavailable`、`converging`、`ready`，并返回每一跳的 Agent 在线、控制证书/中继证书、期望与已应用配置版本及多跳中继应用状态；线路页会显示不可用原因和每跳收敛进度。

## 转发规则开发状态

普通用户可通过 `GET/POST /api/v1/forward-rules` 和 `GET/PATCH/DELETE /api/v1/forward-rules/{id}` 创建、查看、改名、启停及删除自有的转发规则。入口节点必须具备 `forward` 能力且属于有效订购授权的资源组；目标可为授权节点或公网地址。公网地址规则可选绑定已启用的多跳线路，入口必须是线路首跳，整跳节点组与跳数受套餐限制。管理员通过 `/api/v1/admin/forward-target-policies` 批准目标类型、节点组、协议和目标端口范围；默认无授权。TCP、UDP 与 BOTH 分别原子占用对应端口，每节点规则数受订购快照约束；停用保留端口，删除释放端口。端口、目标和线路变更需删除后重建。线路绑定转发的迁移 24 与 PostgreSQL 16.10 集成测试已通过，并发布到 `us bwg`；公网纯 HTTP 预览保持关闭写路由。

## Trojan 代理连接开发状态

`proxy_accesses` 迁移 8 提供每连接随机凭据、Trojan SHA-224 摘要和 AES-256-GCM 加密存储。创建与启用时复核当前有效订购、线路每跳节点能力和资源组授权；连接支持绑定多条共享同一入口节点的候选线路，并按套餐上限、优先级和权重管理。用户只能查询和管理自己的连接，凭据只经专门的授权响应返回；默认线路失效时，查看凭据、轮换凭据和订阅导出会检查可用候选，删除候选会保护仍有活跃计费会话的冻结线路。创建、轮换、更新和删除写审计及待收敛事件。启用浏览器身份认证前必须提供权限为 `0600` 的绝对路径 `CONTROL_PROXY_CREDENTIAL_KEY_FILE`，文件内容为 32 字节密钥的无填充 base64url 编码。缺少密钥时启动会拒绝开启浏览器身份路由。控制面能按节点和授权生成哈希凭据配置，经 mTLS 下发给 Agent；Agent 使用单独的 TLS 服务端证书执行 Trojan TCP CONNECT，仅允许公网目标，凭据轮换和停用可断开旧连接。订阅、用量计费和额度租约已有实现；正式环境仍需节点入网及真实流量验收。

Agent 的代理证书使用 `CONTROL_AGENT_PROXY_CERT_FILE` 与 `CONTROL_AGENT_PROXY_KEY_FILE` 指定，必须是绝对路径，私钥权限不超过 `0600`。Agent 收到代理配置但没有 TLS 服务端证书时会 NACK 并保留旧配置。控制面每分钟续期五分钟的执行配置租约；断线或租约到期时 Agent 关闭监听，套餐到期也会关闭相应连接。该租约不代表流量额度控制。

## Agent 转发运行时开发状态

`internal/agentruntime` 实现独立的 TCP/UDP 直达转发执行器。调用方传入带递增版本号的完整规则快照；运行时先验证目标和端口、预绑定所有新增监听，失败时保留上一个版本。TCP 与 UDP 在连接时解析目标并拒绝私网、回环和保留地址；TCP 连接数、UDP 待处理包与客户端关联数均有上限。停用规则或更换目标会撤销旧 TCP 连接及 UDP 关联；UDP 关联按客户端活动时间过期。运行时支持注入 DNS/拨号器以进行真实套接字测试。Agent 进程已接入运行时与 mTLS 配置流；线上 `us bwg` 的 Agent TLS 仍只在控制面回环地址开放，尚未部署正式 Agent 或执行正式转发。当前纯 IP HTTP 预览提供只读页面与健康检查。

`internal/orchestration` 可从 PostgreSQL 的一致性只读快照中编译单节点转发配置。编译时重新检查账户、有效订购快照、资源组、节点能力和目标策略，剔除失效规则；非法目标、损坏的订购快照或监听冲突会被单独排除并返回诊断，避免阻断其他规则的撤销。节点停用时直接生成空配置。输出规则顺序固定。每节点规则数是创建上限；正数上限下调不自动删减已有规则，降为零则撤销该套餐的转发能力。

配置版本仓储在锁定 Agent 后，于同一数据库事务读取当前节点事实并编译完整转发快照。可执行内容的 SHA-256 不变时不新增版本，但会刷新逐规则诊断；变化时递增 `desired_revision`。Agent 回执必须匹配节点、版本和摘要；应用成功才推进 `applied_revision`，失败回执可随重试更新，但已应用版本不会被失败回执覆盖。失败原因有长度和字符限制。收敛 worker 消费转发规则与目标策略的 outbox 事件，并定期扫描 Agent 节点，以处理套餐到期等时间驱动的撤销。Agent 身份、配置流和回执传输已有实现；线上 Agent TLS 仅在回环地址启用、Agent 进程尚未部署，故服务器当前不会执行转发。

`internal/agentproto` 定义版本 1 的 JSON 消息封包和直达转发配置/结果负载。封包限制为 1 MiB，拒绝未知版本、字段、重复 JSON 字段和无效身份；Agent 验证快照有效期及与控制面一致的可执行内容摘要后才能应用。协议已连接到独立 mTLS WebSocket 监听器和 Agent 进程。

`internal/agentidentity` 提供 Agent 入网与续签身份链路：10 分钟一次性令牌只以 SHA-256 存库；Agent 以 Ed25519 CSR 换取绑定节点 URI 的 24 小时客户端证书；签发和令牌消费在同一事务中完成。证书到期前六小时，Agent 使用当前 mTLS 证书和同一私钥签名的 CSR 调用 `POST /api/v1/agent/renew`；控制面事务化保存有限的重叠证书授权，同一父证书重试返回同一证书。Agent 原子替换证书文件，新的 TLS 连接自动加载新证书。证书认证核对 CA、节点 URI、数据库指纹、节点启用状态与吊销状态。管理员须用当前密码重新认证，并经 `POST /api/v1/admin/nodes/{id}/agent-enrollment` 签发一次性令牌；签发记录审计但不保存明文令牌。Agent 经独立 TLS 入口入网和续签；公网纯 IP HTTP 预览不提供这些路由。CA 私钥不写入数据库或响应，Agent 私钥不离开节点。

管理员也可在节点页撤销现有 Agent 身份，或调用 `PATCH /api/v1/admin/nodes/{id}/agent` 并提交 `{"status":"revoked"}`。该操作需要 `agents.write` 与 CSRF，在同一数据库事务中吊销现有证书、续签与中继授权，删除未使用的入网令牌，写入审计与配置收敛事件；重复撤销不重复写事件。旧证书无法再次认证，现有控制流会在身份复核或心跳时断开，Agent 随后关闭数据监听。需要恢复时须重新签发一次性令牌并完成入网。

`cmd/agent` 提供独立 Agent 进程和 `enroll` 命令。入网命令从标准输入读取一次性令牌，在 Agent 本机生成 Ed25519 私钥，经受信任 HTTPS 换取证书，并仅新建权限为 `0600` 的证书和私钥文件。运行进程主动建立 mTLS WebSocket，发送 hello 与 15 秒心跳，接收完整转发快照，由 `agentruntime` 原子应用并回传 ACK/NACK；异常断线会关闭转发监听并重连。证书续签后通过同一控制流的 `certificate_update`/`certificate_update_ack` 交接在线身份，避免正常旧证书到期关闭数据运行时，详见 [Agent 证书交接协议](docs/agent-certificate-handover.md)。心跳采集 Linux `/proc` 的 CPU、内存、网卡字节及本机活动转发连接数。控制面数据库保存最新指标，45 秒无心跳会转为离线。迁移 `000007_agent_presence` 增加指标表。`cmd/agent-token` 可在受限服务器本机为现有管理员和节点签发令牌到新建的私有文件；命令不在标准输出打印令牌。发布包提供可选 `compose.agent-local.yaml`，使用 host network 连接回环 mTLS 并持久化额度租约/用量 outbox；它不会默认启动。多节点实际部署仍待完成；公网 Agent TLS 入口已有显式开关和可选部署文件，但当前纯 IP HTTP 预览继续关闭浏览器登录，Agent TLS 仅在服务器回环地址开放，尚未进行真实节点验收。

管理员审计页通过 `GET /api/v1/admin/audit` 按时间与 ID 倒序分页查看操作元数据，需 `audit.read` 权限；API 不返回审计记录中的前后状态快照。迁移 `000019_audit_pagination` 为该查询增加排序索引。

## 用户生命周期开发状态

管理员可通过 `POST /api/v1/admin/users` 创建普通用户，通过 `GET /api/v1/admin/users` 分页查看用户，并通过 `PATCH /api/v1/admin/users/{id}` 停用或恢复普通用户。停用事务撤销现有浏览器会话、记录审计并触发 Agent 配置收敛；恢复不会复活旧会话。创建请求的初始密码仅用于生成 bcrypt 哈希，不进入响应或审计记录。用户通过 `POST /api/v1/me/password` 提交旧密码和新密码，成功后所有浏览器会话在同一数据库事务中撤销，当前 Cookie 也会清除。登录会话创建与密码轮换使用用户行锁避免旧密码并发登录；改密尝试每账户限 5 次/5 分钟。创建与改密接口在正式 HTTP 或 HTTPS 模式下需要有效会话与 CSRF 令牌；只读预览关闭这些路由。

## 身份认证与首次管理员

代码提供 `POST /api/v1/auth/login`、`POST /api/v1/auth/logout`、`GET /api/v1/me` 和 `GET /api/v1/me/permissions`。会话使用 12 小时有效的随机令牌，数据库只存哈希；Cookie 为 host-only，带 `HttpOnly`（会话）和 `SameSite=Lax`。HTTPS 模式使用 Secure `__Host-` Cookie，HTTP 模式使用 `control_session`/`control_csrf`；写请求使用 CSRF 令牌并校验浏览器来源。登录在单进程内限制为每账户 5 次/5 分钟、全局 60 次/分钟；过期会话每小时分批清理。首次管理员由 `cmd/admin-bootstrap` 创建，邮箱通过 `CONTROL_ADMIN_EMAIL` 提供，密码从非交互标准输入读取且不得少于 12 字节。完整契约见 [OpenAPI](api/openapi/control-plane.yaml)。

服务配置通过 `CONTROL_BROWSER_AUTH_ENABLED=true` 启用身份路由；正式安装器自动设置。`CONTROL_BROWSER_COOKIE_SECURE` 控制 Cookie 策略，HTTP 为 false，HTTPS 为 true，服务代码缺省为 true。成功登录和退出会在数据库事务中写不含凭据的审计。`cmd/admin-bootstrap --status` 查询管理员状态，`--if-needed` 只初始化第一个管理员，重复安装保留原密码。只读预览下身份路由关闭并返回 404。

## 当前交付边界

控制面代码已经覆盖节点、资源域、线路、转发、代理连接、订阅、分流、套餐、账期计费、RBAC、审计、Agent mTLS、配置收敛和多跳 TCP/UDP 数据面。迁移 1–26、REST/OpenAPI、React WebUI、Linux amd64 发布归档和 Docker Compose 文件均在仓库中；项目不包含用户付费、订单、充值或支付模块。

正式部署支持 IP HTTP、可选 HTTPS 和只读预览。HTTP 与 HTTPS 模式均开放登录、写操作及订阅 Token，权限与 CSRF 检查一致。现场真实节点通流验收另做，不作为代码开发前置条件。节点 Agent 使用独立的 mTLS 入口，Trojan 代理使用节点 TLS 证书。用户确认的 MVP 为单跳线路、Trojan over TLS 和上传加下载计费。

当前 `us bwg` 的纯 IP 只读预览见[部署说明](docs/deployment/us-bwg-preview.md)；旧 `us dmit` 的历史部署见[历史记录](docs/deployment/private-preview.md)。预览实例可检查页面与服务状态，不代表完整控制台已经上线。

## 订阅

用户通过 `/api/v1/subscriptions` 创建绑定已有代理连接的订阅；支持多订阅、名称模板、启停、删除及 Token 重置。套餐 `max_subscriptions` 包含停用订阅。Token 使用随机 256 位值，数据库只保存 SHA-256 哈希与带订阅/所有者上下文的 AES-GCM 密文；元数据和审计不包含 Token。

`GET /api/v1/subscriptions/{id}/url?format=clash|mihomo|sing-box|surge` 返回 origin-relative `path`，前端用当前 HTTP 或 HTTPS origin 组合显示和复制。`preview` 输出相同配置。`GET /sub/{token}/{format}` 每次检查有效套餐、资源授权、启停、45 秒 Agent 在线窗口和已 ACK 的凭据摘要；没有可用连接返回 503，不生成直连兜底。控制面日志会脱敏订阅路径，后续 Nginx 的访问日志也需配置相同脱敏规则。公开订阅按 Token 和全局限流；部署 HTTPS 反代时，应在可信 Nginx 层设置按真实客户端 IP 的限流，控制面不信任外部 Forwarded 头。

当前提供 Clash/Mihomo YAML、sing-box 1.12+ JSON 与 Surge 文本配置，均只生成 Trojan TCP、证书校验及代理选择组。Clash 使用本地 HTTP 7890 与 SOCKS5 7891 入口，Mihomo/sing-box 使用 127.0.0.1:7890 混合入口，Surge 使用本地 HTTP 6152 与 SOCKS5 6153 入口。名称模板支持 `{name}`、`{line}`、`{region}`、`{index}`；重复名称自动区分。订阅页提供格式切换、地址复制、Token 重置、启停及预览。生成的 Clash/Mihomo YAML 已用 Mihomo Meta v1.19.31 原生检查验证，sing-box JSON 已用官方 v1.12.0 原生检查验证；Surge 尚无原生客户端验收。管理员可上传带来源、版本和 SHA-256 的 GeoSite/GeoIP 规则集，启用版本在导出时按同一数据库快照展开为可移植域名/CIDR 规则；IPv6 在 Clash/Mihomo/Surge 中使用 `IP-CIDR6`。GeoSite 无活跃版本时拒绝导出。纯 IP 预览保持订阅和浏览器认证入口关闭。

分流规则的线路授权会逐跳检查多跳线路的角色、节点能力、中继端口、资源域和 `max_hops`。
