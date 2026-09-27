# 进度

- 2026-09-27：按用户确认将纯 IP 预览部署到 Termark 资产 `us bwg`（`144.34.238.89`），使用独立目录 `/opt/network-control-plane/releases/release-e69c44d` 与独立 Compose 项目。Linux amd64 发布包 SHA-256 `6a9566331a7b69d4c52e8b4211e91652e9cb6ee1c60b2f2a16f15367c0849e91`，上传后复核一致；服务器新建 PostgreSQL 16.10 数据卷，真实迁移应用至版本 22。API/DB 均 healthy、重启 0，公网首页和 ready 返回 200，明文登录返回 404；正式库用户、节点、Agent 均为 0。原有 Nginx Proxy Manager 80/81/443 与 embyproxy 8787 保持运行。当前仅为只读预览，多跳线路仍不能启用，真实 Agent 通流与 HTTPS 管理入口尚未验收。

- 2026-09-27：多跳正式配置收敛接入的基础阶段：迁移 22 为线路增加持久 `relay_generation`（现有线路默认 1）；控制面在同一配置事务内读取有序多跳拓扑、节点与 Agent 能力、45 秒在线窗口及现行证书授权。设置 0600、32 字节 base64url 的 `CONTROL_RELAY_SECRET_KEY_FILE` 后，配置仓储可以从 PostgreSQL 加密存储读取每条边密钥并编译对应节点路由；未设置时保持该路径关闭。PGlite 验证迁移 22 的 up/down 与拓扑查询语法，尚未完成真实 PostgreSQL 16 迁移及多节点 ACK 门控，因此多跳仍不能通过 API 启用。完整 Go 测试、vet、前端 29 项测试、类型检查与构建通过。Termark 目标资产名仍待用户确认；Mac 锁屏使后续服务器只读检查暂停，服务器未修改。

- 2026-09-27：继续多跳线路收敛前的本地准备。移除将 Agent 配置 revision 错当作线路 generation 的门槛，修复快照编译测试夹具；中继边密钥改为用线路 ID、世代和边位置作为 AES-GCM 附加认证数据，避免密文跨行替换。新增密钥错行、篡改、错密钥和旧格式拒绝测试。此阶段尚未接入 PostgreSQL 拓扑读取与 Agent ACK 门控，未将多跳线路开放启用。用户指定迁移测试 VPS 为 `us bwh`，Termark 搜索未找到同名资产，正在核对 `us bwg` 是否为目标；新服务器尚未部署或修改。

- 2026-09-27：修复 Clash/Mihomo 订阅对 IPv6 地址和受控 GeoIP IPv6 网段的规则类型，统一生成 `IP-CIDR6`；新增回归测试。全量 Go 测试（允许回环端口环境）和 vet、前端 25 项测试、类型检查、生产构建、OpenAPI 解析和 diff 检查通过。已发布 `release-ipv6-cidr-20260927` 到 `us dmit`，包 SHA-256 `c011de4926a43ca0a7538b820d90c13d43dfeecfe0e43d1e7bea48c63621b13f`；正式库备份权限 0600，迁移 17，API/DB healthy、重启 0，首页/live/ready 200，登录和未认证规则集 API 404。清理已有发布目录对应的旧上传归档后磁盘可用约 1.5 GB。
- 2026-09-27：受控 GeoSite/GeoIP 规则集切片完成，提交 `3d4faf4` 并发布到 `us dmit` 的 `release-3d4faf4`。管理员可上传来源、版本、条目和 SHA-256，启用新版本会原子停用旧版本；订阅导出按同一事务展开为域名/CIDR。Go 全量测试、vet、前端 25 项测试、类型检查、构建和 OpenAPI 解析通过；服务器独立 PostgreSQL 16 迁移 1–17 与版本激活/回滚/冲突测试通过。正式库迁移 17、规则集 0 条、用户/节点/Agent 仍为 0；公网首页/ready 200，登录 404。
- 2026-09-27：线路编辑 UI 提交 `1852394` 并发布到 `us dmit` 的 `release-1852394`。前端 23 项测试与生产构建通过；公网 IP 首页/ready 200、HTTP 登录 404，API/DB healthy，迁移 16，正式库用户/节点/Agent 均为 0。开始受控 GeoSite/GeoIP 规则集切片：纯格式生成器的四格式展开测试、领域规范化模型与迁移 17 已着手。

- 2026-09-25：读取设计技能与工作区；确认空仓库，开始架构文档。
- 2026-09-25：完成十部分架构设计及自检；补齐代理连接模型、用户自有线路、配额租约和配置收敛语义；等待用户确认架构。
- 2026-09-25：用户确认架构。检查本机环境：Node 可用，Go 和 Docker 不在 PATH；准备按阶段实施，Go 工具链需临时下载。
- 2026-09-25：在隔离工作区 `codex/control-plane-foundation` 完成配置与 HTTP 基础层；Go 1.27.1 官方归档 SHA-256 校验通过；测试和 vet 通过。SQL 使用 PostgreSQL parser 成功解析，OpenAPI YAML 成功解析；真实 PostgreSQL 迁移尚未运行。本地 HTTP 冒烟验证返回 200/200/404，响应含请求 ID。
- 2026-09-25：临时 PGlite 实例执行 migration up/down，创建 12 张表并成功回滚至 0 张表。第一阶段基础服务已验证；外部 PostgreSQL 16 仍待下阶段接入。
- 2026-09-25：接入 pgx/v5、数据库启动检查、依赖感知的 readiness 和事务化 migration 命令。单元测试、vet、PGlite up/down 通过；Go 到 PostgreSQL 16 的实时连接尚未验证。
- 2026-09-25：`us dmit` 私有预览部署到 `/opt/network-control-plane/releases/2594ba4`。初次迁移因 AppleDouble 文件失败；回归测试先红后绿，修复后 API/DB 均健康，HTTP 探针 200，真实 PostgreSQL 16.10 迁移和重跑成功，原有 Nginx Proxy Manager 未受影响。
- 2026-09-25：按用户要求发布 `2ead2ae`，将 API 临时绑定到公网 18080。开发机经 `179.255.145.149` 请求 `/live` 和 `/ready` 均返回 200；域名与 HTTPS 留待后续 Nginx 反代。当前仍只有基础健康接口，没有管理 UI。
- 2026-09-26：开发身份与 RBAC 基础：版本 2 迁移、bcrypt 登录、哈希会话和 CSRF、安全 Cookie、`/me`、权限查询、UUIDv7 和管理员初始化命令。审查后补充默认关闭身份路由、登录限流、`lines.write.self` 权限分离与过期会话清理。全量 Go 测试与 vet 通过；真实 PostgreSQL 16 独立数据库中的迁移、回滚、会话仓储和管理员事务测试通过。公网预览暂不发布登录代码，正式库仍为迁移版本 1。
- 2026-09-26：`us dmit` 更新至 `c5b1664`。升级前执行 pg_dump 备份；迁移版本 2 应用并重跑成功，API/DB 容器健康，公网 `/live` 和 `/ready` 均 200，`POST /auth/login` 返回 404，身份路由保持关闭。
- 2026-09-26：完成资源组与节点目录 API、授权范围查询和审计迁移。全量 Go 测试、vet、OpenAPI 解析及独立 PostgreSQL 16 集成测试通过；迁移 3 down 已在测试库执行。审查发现的分页契约和纯转发节点端口校验问题已修复。
- 2026-09-26：发布 `43f17c8` 到 `us dmit`。升级前备份正式库，迁移版本 3 应用并重跑成功；公网纯 IP `/live`、`/ready` 返回 200，登录接口保持 404。API/DB 容器 healthy；独立测试数据库与临时文件已清理。
- 2026-09-26：完成套餐创建、资源组/共享单跳线路授权、有效订购创建与冻结快照、用户自查权益 API。真实 PostgreSQL 16 测试覆盖快照不受套餐授权变动影响、私有线路和越权节点组拒绝、重复订购冲突、期限边界及节点目录联动；Go 全量测试、vet 与 OpenAPI 解析通过。发布 `33201ce` 到 `us dmit`，公网健康探针 200、登录和套餐接口 404，浏览器认证继续关闭。
- 2026-09-26：完成普通用户创建、列表和自助改密。审查发现并修复登录与密码轮换的会话竞争、无业务权限用户无法改密、改密缺少限流。真实 PostgreSQL 16 集成测试包含受控行锁等待；完整 Go 测试、vet、OpenAPI YAML 解析通过。提交并发布 `93f8c0b` 到 `us dmit`，正式库升级前备份权限 0600、迁移仍为 1–3；公网健康接口 200，登录和用户接口 404，API/DB healthy。独立测试库和临时测试程序已清理。
- 2026-09-26：完成单跳线路 API，提交 `b51c548` 并部署到 `us dmit`。正式库升级前备份 `pre-lines-b51c548.dump`；公网健康接口 200，登录与线路接口 404，API/DB healthy。部署记录提交 `ecd26d1`。
- 2026-09-26：开始直达转发规则切片。复核已批准的架构、现有 SQL/RBAC/HTTP 模式和干净的隔离工作区；完整 Go 基线测试通过。新增实施计划 `docs/superpowers/plans/2026-09-26-direct-forward-rules.md`。
- 2026-09-26：完成直达 TCP/UDP/BOTH 转发规则、原子端口预留、目标策略默认拒绝、订购限制、审计/outbox 和 REST/OpenAPI。策略撤销会标记相关规则待收敛。回归测试先复现已禁用账户仍可重新启用规则，再修正仓储锁与状态校验。完整 Go 测试、vet、OpenAPI 解析通过；真实 PostgreSQL 16 独立库重建两次，转发集成测试均通过。公网认证继续关闭，实际 Agent 执行尚未接入。
- 2026-09-26：提交 `235abdf` 并发布到 `us dmit`；正式库先备份至 `pre-forward-235abdf.dump`，迁移版本 4 已应用。API/DB 容器 healthy、重启次数 0；公网纯 IP 健康接口均为 200，登录和转发管理接口均为 404。专用测试数据库及临时二进制已清理，现有 Nginx Proxy Manager 的 80/443 端口未变。
- 2026-09-26：继续完整项目开发。现有隔离分支为 `codex/control-plane-foundation`，工作树干净；全量 Go 基线测试通过。确认转发控制面尚无 Agent 执行器，新增 Agent 直达转发运行时实施计划。
- 2026-09-26：完成传输无关的 Go Agent TCP/UDP 直达转发运行时及真实套接字测试。测试先复现慢 DNS 阻塞 UDP 其他客户端，改为有上限的并发处理；另复现 TCP 上游关闭后空闲客户端滞留，加入可配置收尾期限。包测试及三轮 race 检查通过；尚未接入控制面流或部署 Agent。
- 2026-09-26：审查后补齐停用及更换目标时撤销活动 TCP 流、UDP 按客户端活动到期、旧 UDP 回复在版本切换后丢弃、并发关闭与 UDP 回复协程等待的回归测试与修复。最后一次修复后全量 Go 测试、三轮运行时 race 测试、vet 与差异检查均通过；本地组件仍未连接控制面或服务器 Agent。
- 2026-09-26：提交 Agent TCP/UDP 运行时 `429177a`。复查纯 IP 预览，API/DB healthy，公网健康接口 200，登录仍 404。开始节点转发快照编译器；PostgreSQL 16 隔离库验证有效规则、策略撤销和订购到期过滤，测试库与上传二进制已清理。Agent 版本持久化与通信仍待开发。
- 2026-09-26：快照编译器审查后改为单条坏规则隔离，节点停用直接输出空快照，目标节点优先使用公网 IP；冻结套餐额度为零会撤销转发能力。PostgreSQL 16 集成测试在独立库连续两轮通过且测试资产已清理；全量 Go 测试、vet、差异检查通过。快照尚无版本持久化与下发链路。
- 2026-09-26：新增配置版本存储与迁移 5。测试先覆盖版本去重、ACK/NACK 与乱序结果，再按审查意见移除外部 `Stage`，改为事务内锁定 Agent、读取节点事实并编译；相同摘要刷新诊断，重复失败回执可更新，错误内容受限。新迁移和并发回归在独立 PostgreSQL 16 测试库连续五轮通过；本地完整 Go 测试、vet 和差异检查通过。正式库未迁移，线上仍是纯 IP 健康预览，Agent 通道尚未实现。
- 2026-09-26：开始 Agent 通信封包切片。版本 1 JSON envelope 限制 1 MiB，严格校验 ID、时间、消息类型、未知字段与重复字段；直达转发快照校验版本、有效期、规则和 SHA-256，配置结果限制失败文本。控制面版本存储与 Agent 共用同一可执行配置编码，避免摘要漂移。传输、入网与心跳仍待开发。
- 2026-09-26：新增 React/TypeScript/Tailwind 纯 IP 只读预览页，七项导航、浅深主题、手机布局、真实数据库就绪轮询和模块空状态。Go 静态包装保留 `/api/*` 路由，并对缺失资源返回 404；浏览器身份路由仍关闭。已在本地浏览器检查手机导航、节点页面和主题；部署验证待完成。
- 2026-09-26：发布 `a056e72` 到 `us dmit`，升级前 pg_dump 备份权限 0600；迁移 5 应用，公网首页和就绪探针 200，登录 404。真实浏览器加载服务器 IP 页面，状态显示“运行正常”；API/DB 容器 healthy、重启次数 0，既有 Nginx Proxy Manager 未受影响。Agent 通道和可操作控制台仍待开发。
- 2026-09-26：实现配置收敛 worker：规则事件按入口节点重算，目标策略事件重算 Agent 节点；`SKIP LOCKED` 领取、失败有界退避，周期扫描覆盖套餐到期等无事件撤销。测试先因缺少 worker API 失败，随后在独立 PostgreSQL 16 测试库两轮通过；完整 Go 测试、vet、race 与前端构建通过。此切片只生成期望版本，Agent 通道和实际转发仍待接入。
- 2026-09-26：发布 `ce02780` 到 `us dmit`。正式库升级前备份权限 0600；公网纯 IP 首页与就绪探针 200，登录 404，API/DB 容器 healthy 且重启次数 0。迁移维持版本 5；正式库 Agent 数为 0，现有 Nginx Proxy Manager 继续运行。独立测试库和临时测试二进制已清理。
- 2026-09-26：开始 Agent 入网身份链路。实现 Ed25519 CSR 校验和节点绑定客户端证书、一次性令牌事务、数据库指纹与吊销复核、管理员令牌路由、独立 TLS 入网监听器及可选 TLS 配置校验。迁移 6 先在独立 PostgreSQL 16 测试库验证；公网预览继续关闭入网和浏览器登录，Agent 流与运行进程仍待开发。
- 2026-09-26：审查后补齐管理员当前密码重新认证、令牌签发审计、锁等待与签发期间的到期复核，以及 Agent 入网 IP/令牌限流。全量 Go 测试、vet、重点 race 测试和 OpenAPI 解析通过；独立 PostgreSQL 16 测试库再次验证入网和迁移 up/down。提交 `74fc614` 并发布到 `us dmit`，正式库备份后迁移至版本 6。公网首页/就绪 200，登录和入网入口 404；Agent 数为 0，实际转发尚未部署。
- 2026-09-26：继续完整项目，开始 Trojan 代理连接切片。开发分支新增迁移 8、凭据 AES-256-GCM 加密、Trojan SHA-224 摘要、代理连接创建/查询/轮换/启停/删除与 REST/OpenAPI。数据库事务记录审计和不含凭据的 outbox 事件；浏览器认证开启时要求 0600 密钥文件。独立 PostgreSQL 16 测试库完成迁移 8 up/down 和代理连接生命周期、并发重名、越权、订购到期验证；正式库尚未迁移 8，Agent 代理数据面与订阅仍待实现。
- 2026-09-26：代理 Agent 阶段 `e4b5e56` 经完整 Go 测试、重点 race、vet、OpenAPI 解析、前端构建及代码审查后发布到 `us dmit`。正式库升级前备份 `pre-proxy-e4b5e56.dump` 权限 0600；迁移至版本 9。公网首页与健康接口 200，登录及 Agent 入网 404；API/DB healthy，API 重启 0，Agent 数 0。尚无流量额度执行、订阅和真实节点，代理服务未开放。
- 2026-09-26：订阅模块开发分支新增迁移 10、同用户目标复合外键、Token 哈希与加密、套餐数量限制、Mihomo/sing-box 导出、管理 REST 和公开 Token 路径。导出只显示当前授权、Agent 在线且凭据已 ACK 的代理连接；Token 路径日志脱敏并拒绝非规范路径重定向。服务器独立 PostgreSQL 16 测试库验证迁移 10 up/down/up、订阅生命周期、并发限额、撤销、离线及轮换过滤；测试库已清理。全量 Go、订阅/HTTP race、vet、OpenAPI 和前端构建通过。正式库仍为迁移 9，订阅版本尚未部署。
- 2026-09-26：订阅阶段 `a40cd29` 发布到 `us dmit`。正式库升级前备份 `pre-subscriptions-a40cd29.dump` 权限 0600；迁移至版本 10。公网首页、健康 200，登录/订阅管理/公开订阅/Agent 入网 404；订阅 404 带 no-store。API/DB healthy，API 重启 0，Agent 数 0，Nginx 80/443 未变。真实代理、额度账本、分流和可操作控制台尚未完成。

- 2026-09-26：实现 billing 日历与用量账本基础：31/30/29 锚定日、短月恢复、时区/DST、累计整数倍率差额与溢出检查；迁移 11 新增账期/连接会话/只追加事件，并冻结订购周期月数。独立 PostgreSQL 16 库验证 up/down/up、并发幂等、冻结倍率、旧账期归属、纳秒重试与聚合溢出回滚，测试库已删除。独立审查发现微秒存储导致重试冲突，真实库复现后修复。全量 Go、billing race 和 vet 通过；新代码尚未部署，正式库仍迁移 10。下一步是额度租约、周期 worker、Agent 用量上报与本地额度执行，随后 REST/UI。完整项目继续进行，不做支付系统。

- 2026-09-26：完成额度租约与账期周期推进阶段。迁移 12 增加租约和账期激活字段，多个 Agent 使用账期行锁预留计费字节，幂等重试不重复预留，过期未对账租约不自动释放。用量报告消耗租约并计入账本，显式结算释放剩余；实际边界超额如实记账。账期 worker 激活冻结授权并发出收敛事件，历史账期使用原始快照。独立 PostgreSQL 16.10 迁移 up/down/up、并发额度与旧账本/套餐/编排集成测试通过；全量 Go、billing/orchestration race、vet 通过。真实 Agent 流量执行仍未接入，不能开放代理。

- 2026-09-26 发布 `2e87597`：账期、用量账本、计费字节租约与周期 worker 纳入镜像。正式库先备份，随后应用迁移 11/12；Agent 数 0，API/DB healthy，重启次数 0。公网首页与 live/ready 200，登录、订阅管理、公开订阅及 Agent 入网 404；公开订阅路径返回 no-store。现有 Nginx Proxy Manager 80/443 未改动。Agent 上报与本地额度执行、分流、可操作前端及真实节点验收仍待完成。

- 2026-09-26：Agent 用量报告子阶段新增 `usage_batch`/`usage_ack`、严格限长与字段校验、SHA-256 批次摘要、认证节点到 Agent ID 的控制面入账映射、服务端先持久化再 ACK。Agent 有 0600 文件 outbox、排他锁、原子落盘、失效拒写、断线重放和持续连接发送；ACK 丢失会重发同批次，已入账项由连接序号去重。修复配置保存阻塞时租约到期执行器仍运行、重入网同配置 ACK 未恢复 applied revision、账期截止时刻最终快照被拒绝。协议见 `docs/agent-usage-protocol.md`。独立 PostgreSQL 16 容器验证真实账本、节点映射、账期边界和重入网回归；Go 全量测试、相关 race、vet 通过。独立测试容器已删除；正式服务器未发布本轮代码，继续纯 IP 只读预览。连接准入、租约申请与结算及 TCP/UDP/Trojan 本地计量/限额仍待实现，真实代理不得开放。
- 2026-09-26：连接准入阶段新增迁移 13，将每份计费租约唯一绑定连接；控制面按 mTLS 节点身份核对已应用配置、资源所有者、冻结账期与倍率，处理开启/续租/结算消息。独立 PostgreSQL 16 临时库验证 migration 13 up/down/up、并发幂等、跨连接借用拒绝、资源转移、配置推进和线路迁移；完整 Go 测试、关键 race 与 vet 通过。Agent 客户端、本地计量和真实节点执行尚未接入，正式服务器继续只读预览。
- 2026-09-26：Agent 客户端增加额度请求 ID 关联与单读者回复分发，测试覆盖重复请求、拒绝、取消及断线清理；生产 Agent 在额度执行尚未接通时拒绝含真实流量的配置快照。连接签发时刻和心跳时间校验经独立 PostgreSQL 回归修正。执行器逐字节计量、额度耗尽停用、持久化未结租约和真实节点部署仍待完成。
- 2026-09-26：抽出 Agent/控制面共用的累计整数计费公式，新增 Agent 并发预算、按实际写入计量、UDP 整包拒绝，以及执行器的传输无关计量接口。真实套接字测试覆盖 TCP、Trojan TLS、UDP 在模拟计量服务下的准入和额度停止，完整 Go 测试及 vet 通过。生产 Agent 仍不安装真实计量服务，故继续拒绝开通流量；未结租约持久化、续租、用量报告/结算和服务端部署仍待实现。
- 2026-09-26：新增 Agent 活动租约文件存储原语：0600 权限、独占锁、严格 JSON 字段校验、原子落盘、重启恢复、租约身份不可变、累计计数和序号不可回退、结算删除。针对损坏/重复字段/符号链接/权限错误/并发打开的测试通过；尚未接入运行时报告 ACK 和续租状态机，不能开放真实流量。
- 2026-09-26：完成 Agent 连接级额度续租的首个闭环：保留读取缓冲区中的未发送字节，旧租约最终用量 ACK 后结算，再申请新租约；累计计数和序号跨租约连续，结算量只包含当前租约增量。关闭会唤醒等待 ACK 的复制，TCP/Trojan 会话撤销时同步关闭计量器；重启恢复按当前租约增量计费。新增当前账期、用户 UTC 日报和管理员按日期/用户/入口节点/线路聚合的 REST、RBAC、范围限制与 OpenAPI 文档。Go 全量测试、Agent/runtime race、vet、前端构建通过；PGlite 校验了账期与报表 SQL。独立 PostgreSQL 16 和真实服务器验收尚未做，线上继续纯 IP 只读预览。

## 2026-09-26 分流与稳定性阶段

- 新增迁移 14：`routing_profiles` 与 `routing_rules`，支持域名、域名后缀、IP、CIDR、GeoIP、GeoSite 规则；fallback 支持直连、阻断或套餐授权的单跳线路。
- 新增 `internal/routing` 领域校验、PostgreSQL 仓储、套餐规则数量限制、线路授权复核、审计记录与 profile 校验 API。
- 新增用户分流 REST：`/api/v1/routing-profiles` 及 rules、validate 子资源；接入 `routing.read` / `routing.write` RBAC 和 CSRF。
- 修复 Agent 配置撤销时的计量结算死锁：先关闭数据连接，再异步等待控制面结算 ACK；新增回归测试。
- 前端首页支持安全来源下登录后读取套餐、账期和最近日流量；公网纯 HTTP 继续保持只读预览。
- 验证：全量 Go 测试、`go vet ./...`、Agent/分流/API race 测试、Web 测试/typecheck/build 均通过。PostgreSQL 16 独立迁移和服务器预览验收见下文。

### 服务器预览发布（2026-09-26）

- Termark 资产 `us dmit` 已确认地址为 `179.255.145.149:22`；通过应用内 CLI 完成发布。
- 发布目录：`/opt/network-control-plane/releases/preview-20260926`；发布前数据库备份：`/opt/network-control-plane/backups/ncp-before-preview-20260926.dump`。
- 服务器 PostgreSQL 16.10 临时库已验证迁移 1–14，随后删除；正式预览库已由迁移 worker 升级到版本 14。
- 纯 IP 预览已切换到新镜像并健康运行：`http://179.255.145.149:18080/`；`/api/v1/health/ready` 返回 200，容器状态 healthy，`/api/v1/me` 返回 404（浏览器认证保持关闭）。
- Nginx/HTTPS、登录、Agent 入网和真实代理流量仍未开启；域名反代后再单独启用浏览器认证。

## 2026-09-26 订阅与分流联动

- 新增迁移 15，将订阅可选绑定同用户分流 Profile；创建和更新时检查归属与启用状态，PATCH `null` 可以清除绑定。
- Mihomo 导出按优先级生成域名、后缀、IP、CIDR、GeoIP 规则及 fallback；sing-box 导出域名、后缀、IP、CIDR 规则。GeoSite 暂无受控数据源，两个格式均明确拒绝；sing-box GeoIP 同样明确拒绝。线路动作必须在本次订阅实际可用目标中找到对应线路，Profile 停用或线路缺失时不导出错误配置。
- 全量 Go 测试、vet、前端测试/类型检查/构建及 OpenAPI YAML 解析通过。服务器独立 PostgreSQL 16 测试库完成迁移 1–15、订阅生命周期测试和迁移 15 down/up，测试库已删除。
- 已通过 Termark 发布到 `us dmit` 的 `/opt/network-control-plane/releases/preview-routing-20260926`；正式预览库升级前备份 `/opt/network-control-plane/backups/ncp-before-routing-20260926.dump`（0600），迁移版本 15。API/DB 健康且重启 0；公网纯 IP 首页和就绪接口 200，登录 404。HTTPS 域名、浏览器认证、Agent 入网和真实节点端到端验收仍待完成。

## 2026-09-26 控制台操作页

- 登录后的节点与线路页接入真实分页 API；节点展示资源域、能力、地址、倍率、Agent 状态和心跳，线路展示拓扑、优先级、权重、倍率，并支持用户自有单跳线路创建、启停。
- 转发页接入真实规则列表和创建向导，支持 TCP、UDP、BOTH、节点或公网目标、CSRF 和服务端错误反馈。
- 订阅页接入代理连接、分流 Profile 和订阅管理；支持创建代理连接、凭据查看/轮换/启停、创建订阅、绑定 Profile、地址查看、Token 重置和订阅启停。凭据与 Token 仅在明确操作后显示，不写入浏览器存储。
- 分流页接入 Profile 和规则创建/查看，支持 fallback、优先级、domain/domain_suffix/IP/CIDR/GeoIP/GeoSite；最终格式支持边界仍由服务端校验。
- 新增前端分页、CSRF、权限/空状态测试；前端测试、类型检查和生产构建通过。纯 IP 预览已更新到 `/opt/network-control-plane/releases/subscriptions-ui-20260926`，迁移保持版本 15，公网首页/就绪 200，登录 404，API/DB healthy 且重启 0。
- 控制台操作页提交 `36ea4e4` 已通过 Termark 发布到 `/opt/network-control-plane/releases/release-36ea4e4`。发布包不含 `.env` 或 `.git`；沿用服务器原有 0600 `.env`，Compose 配置校验通过。公网纯 IP 首页与 ready 均为 200，登录为 404；正式库迁移保持版本 15，API/DB healthy、重启次数 0，现有 Nginx Proxy Manager 继续运行。当前 HTTP 入口仍只提供只读预览；登录后可操作页须待 HTTPS 和浏览器认证启用后验收。

## 2026-09-26 管理员与账户页面

- 管理员入口按权限显示，接入用户、套餐、套餐授权、资源域和节点 API；套餐创建需要明确勾选资源域和共享线路，避免默认扩大授权。账户页显示角色与权限并提供 CSRF 自助改密；密码不进入浏览器存储。
- 请求映射测试先失败后通过；本地模拟安全会话下用 Edge/Playwright 检查桌面与手机页面。手机套餐弹窗授权列表原被压缩，修复后实测高度 62px。上线的 HTTP IP 入口仍保持只读预览；真实管理员操作需待 HTTPS 启用后验收。
- 管理员与账户页提交 `36322b0` 已通过 Termark 发布到 `/opt/network-control-plane/releases/release-36322b0`。发布包 SHA-256 在本地与服务器一致，服务器 `.env` 权限 0600，Compose 配置校验通过。切换时首个 HTTP 请求短暂连接重置，随后 API/DB 均 healthy、API 重启 0；公网首页与 ready 返回 200、登录返回 404，正式库迁移保持 15，既有 Nginx Proxy Manager 正常运行。浏览器认证仍未开启，真实写操作尚未在服务器验收。

## 2026-09-26 Surge 订阅与格式切换

- 提交 `abac794` 新增 Surge 文本配置导出，支持 Trojan TCP、线路选择组、Domain/IP/CIDR/GeoIP 规则与 fallback；对配置分隔符、密码和 IPv6 地址进行校验。订阅页新增 Mihomo、sing-box、Surge 格式切换，地址查看、Token 重置与预览使用当前格式。
- 完整 Go 测试、vet、15 项前端测试、TypeScript 类型检查、生产构建、OpenAPI YAML 解析及 diff 检查通过。Surge 原生客户端二进制兼容性尚未验证。
- 通过 Termark 发布到 `/opt/network-control-plane/releases/release-abac794`。发布包 SHA-256 为 `7936b81d18854eaff78bef9be51cc82e84bbdc23458d98913182cb10229b87d0`；正式库备份 `/opt/network-control-plane/backups/ncp-before-abac794.dump` 权限 0600，迁移保持 15。API/DB 均 healthy、重启次数 0，公网首页与 ready 200、登录 404；原有 Nginx Proxy Manager 继续运行。浏览器认证、Agent TLS 和真实节点端到端验收仍待完成。

## 2026-09-26 管理员转发目标策略

- 提交 `5c297c2` 将已有管理员转发目标策略 API 接入页面，支持按目标类型、资源域、TCP/UDP 和端口范围创建，列出策略并启停。只有 `forward_policies.write` 的管理员可以读取资源域以选择节点目标，仍不能创建资源域。
- Go 全量测试、vet、16 项前端测试、TypeScript 类型检查、生产构建、OpenAPI YAML 解析及差异检查通过。本地模拟管理员会话使用 Edge/Playwright 检查桌面策略卡片和手机创建弹窗，临时浏览器及服务器已关闭。
- 通过 Termark 发布到 `/opt/network-control-plane/releases/release-5c297c2`，发布包 SHA-256 为 `a824694b884dc620bde1086037f899e5637837d3da9439396662c06cb934e621`。正式库备份 `/opt/network-control-plane/backups/ncp-before-5c297c2.dump` 权限 0600，迁移保持版本 15。API/DB 均 healthy、重启 0，公网首页和 ready 200、登录 404，现有 Nginx Proxy Manager 正常。策略写操作仅在 HTTPS 和浏览器认证启用后可用；真实节点入网及代理流量仍未验收。
- 2026-09-26：管理员节点指标与 Agent 入网令牌功能提交 `cc34192`，通过 Termark 发布到 `/opt/network-control-plane/releases/release-cc34192`。节点指标仓储在服务器独立 PostgreSQL 测试库运行通过，测试库已删除；本地完整 Go 测试、Go vet、前端 20 项测试、类型检查、构建及 OpenAPI 解析通过。发布包 SHA-256 `3a35bba658da6b456b8928d82414dd7da5373ababddb577b4d232bc2cc6e101b`；正式库备份 `/opt/network-control-plane/backups/ncp-before-cc34192.dump` 为 0600，迁移保持 15。公网 IP 首页、live、ready 返回 200，登录返回 404；API/DB healthy、重启 0。正式库节点和 Agent 仍均为 0，浏览器认证与 Agent TLS 未开启，真实代理和转发仍未验收。
- 2026-09-26：审查修复提交 `d8149f1`，重新入网时事务化清除旧 Agent 指标，并将前端 15 秒指标轮询改为请求完成后再安排下一次。独立 PostgreSQL 测试库回归测试先失败后通过并自动清理；完整 Go 测试、vet、前端测试、类型检查、构建通过。经 Termark 发布到 `/opt/network-control-plane/releases/release-d8149f1`，包 SHA-256 `d4dd482252a61e7b0f0e2e7ee1bb1ab0b01d4017d076e9b9fcf8c347db1f8aa2`。正式库备份 `/opt/network-control-plane/backups/ncp-before-d8149f1.dump` 权限 0600，迁移版本 15；公网 IP 首页、live、ready 200，登录 404，API/DB healthy、重启 0，节点和 Agent 均仍为 0。
- 2026-09-26：快速重连旧指标问题再次用独立 PostgreSQL 测试库先复现后修复，提交 `0e01dec`。指标只在采集时间不早于本次 Agent 上线/心跳时间时标记实时。通过 Termark 发布到 `/opt/network-control-plane/releases/release-0e01dec`，包 SHA-256 `42c23d6a68d7b82fbec08dadd6b3a4e6865e8a0a2526fd50b1c3740fc8201d9a`。正式库备份 `/opt/network-control-plane/backups/ncp-before-0e01dec.dump` 权限 0600；迁移版本 15，公网 IP 首页和探针 200、登录 404，API/DB healthy、重启 0，正式库仍无节点及 Agent。
- 2026-09-26：节点启停提交 `011d97a` 并经 Termark 发布到 `us dmit`。本地完整 Go 测试、vet、前端 20 项测试、类型检查、构建和 OpenAPI 解析通过。正式库备份 `ncp-before-011d97a.dump` 为 0600；发布包 SHA-256 `24347f9861ccf3b68daf04c42ce4acb8a7744378888b5cbb863308c702a43fcd`。公网首页/live/ready 200、登录 404，API/DB healthy 且重启 0，迁移 15，节点/Agent 均 0。首个真实节点位置待用户确认。
- 2026-09-26：提交 `1f98d29` 新增显式 Agent TLS IP 监听开关与可选 Compose 覆盖文件；完整 Go 测试、vet、前端 20 项测试、类型检查、构建通过，服务器 Compose 合并配置验证默认主机回环映射。发布到 `us dmit` 的 `release-1f98d29`，升级前备份 `ncp-before-1f98d29.dump` 权限 0600。专用 Ed25519 CA 与服务器证书在服务器受限目录生成，CA/IP 校验和回环 HTTPS Agent 入网路由验证通过；18443 仅监听 127.0.0.1。公网首页/live/ready 200、登录 404，API/DB healthy、重启 0，迁移 15，节点/Agent 仍为 0。首个真实节点位置待确认。
# 2026-09-27 Agent 证书续签

- 提交 `0313ce5` 增加 mTLS 证书续签、同密钥 CSR 校验、有限重叠授权、父证书幂等重试、过期清理和 Agent 自动落盘。服务端先验证数据库指纹与节点状态，再判断六小时续签窗口；Agent 新连接从证书文件重新加载身份。
- Go 全量测试、vet、前端 20 项测试/类型检查/生产构建、OpenAPI YAML 解析通过；独立 PostgreSQL 16 库验证身份全套集成测试、迁移 16 down/up。TLS 客户端测试覆盖同密钥 CSR、响应身份和证书替换。独立库已清理。
- 经 Termark 发布到 `us dmit` 的 `release-0313ce5`，正式库备份权限 0600，迁移版本 16，API/DB healthy、API 重启 0。公网首页/ready 200，HTTP 登录和续签 404，回环 mTLS 无证书续签 401；正式库用户、节点和 Agent 数均为 0。
- 仍待 HTTPS 浏览器管理入口、正式管理员/节点授权入网和真实 TCP/UDP/Trojan/计费端到端验收。现有控制流到期重连会关闭数据运行时，活跃连接中断；无中断交接需单独实现。

## 2026-09-27 Agent 证书在线交接

- 提交 `72d9c73`：续签后的 Agent 在既有 WebSocket 发送新证书；控制面验证同节点、同 Ed25519 公钥及当前证书授权后，切换连接的周期身份复核目标。Agent 忽略无关 ACK，保留待确认状态并重试。
- ACK 错误指纹回归先失败后通过；真实 TCP 转发长连接测试确认旧证书过期前后同一连接仍能收发。Go 全量测试、vet、关键路径 race 测试、前端 20 项测试、类型检查/生产构建及 OpenAPI YAML 解析通过。UDP/Trojan 长连接和真实数据库授权交接尚未做集成验收。
- 经 Termark 发布到 `us dmit` 的 `release-72d9c73`；上传包 SHA-256 与本地一致，升级前正式库备份权限 0600。API/DB healthy、API 重启 0、迁移 16；服务器回环首页/ready 200、明文登录 404、无证书续签 401。正式库仍为 0 用户、0 节点、0 Agent；未启动 Agent overlay，无法进行生产节点会话验收。
- 后续将证书交接集成测试扩展为真实 TCP、UDP 和 Trojan TLS 会话，均在旧证书过期前后使用同一客户端连接收发。新增真实 PostgreSQL 授权测试，覆盖未授权、授予、Agent 吊销、节点停用和授权删除；在服务器独立迁移 16 测试库通过，测试库与二进制已清理，正式库未写入。全量 Go 测试、关键路径 race 测试及 vet 通过。本轮仅增加测试，线上运行版本仍为 `72d9c73`。

## 2026-09-27 普通用户停用与恢复

- 新增管理员用户状态操作：事务内停用普通用户、撤销浏览器会话、写审计与 `user.changed` outbox；恢复不会复活旧会话。配置收敛 worker 消费事件后重算 Agent 快照，撤销停用用户的转发与代理配置。前端增加停用/恢复操作，并在新订购表单中排除停用账户。
- 在服务器独立 PostgreSQL 16 测试库通过账户状态和 Agent 转发撤销集成测试；临时库与测试二进制已清理，正式库仍为 0 用户、0 节点、0 Agent。Go 全量测试、相关包 race 测试、vet、前端 20 项测试、生产构建和 OpenAPI YAML 解析通过。
- 经 Termark 发布到 `us dmit` 的 `release-34f44bc`，发布包 SHA-256 `8ccc8bbe1ce891fe2f5c64fed4e1bd44a8a93ff180cd683e20ba9e9609861580`。升级前正式库备份 `/opt/network-control-plane/backups/ncp-before-34f44bc.dump` 权限 0600；迁移版本 16，API/DB healthy 且重启 0。公网 IP 首页、live、ready 均为 200，明文登录 404，回环 mTLS 续签在无证书时为 401；现有 Nginx Proxy Manager 仍运行。
- 公网纯 IP 入口仍关闭浏览器认证；正式管理员、节点与 Agent 尚未创建，真实数据面和计费闭环不能在正式环境验收。
- 在服务器上使用独立 PostgreSQL 测试库完成真实 Agent 冒烟：创建临时管理员和 forward 节点，签发一次性令牌，完成 mTLS 证书登记；Agent WebSocket 上线并成功应用配置版本 1。测试数据库、临时 Agent 控制面容器、证书和脚本均已删除；正式库用户、节点和 Agent 计数仍为 0。
- 继续在 `us dmit` 的独立 PostgreSQL 16 测试库完成真实 TCP 数据面验收：临时 Agent 应用含转发规则的配置版本 1；访问 `127.0.0.1:24010` 得到目标 `179.255.145.149:18080` 的 `{"status":"ok"}`；用量账本记录上传 98、下载 172、计费 270 字节。随后删除临时 API 容器、测试库、Agent 证书及脚本，正式库用户、节点、Agent 计数仍均为 0，正式 API/DB 保持 healthy。
- 隔离 UDP 验收先确认数据报回环成功；考虑 UDP 关联默认 60 秒空闲结算后重跑，账本落库 1 条事件，上传 13、下载 13 字节。Termark 单次命令 60 秒超时只影响脚本等待返回，不影响已核对的数据库结果；随后删除临时数据库、容器、工作目录和脚本，正式库计数仍为 0。
- 本地验证：在受限沙箱中运行 Go 全量测试会因回环监听权限失败；使用允许回环网络的执行环境后 `go test ./... -count=1` 全部通过。前端 `npm --prefix apps/web test` 20 项通过，`npm --prefix apps/web run build` 成功，`go vet ./...` 成功。
- 独立 PostgreSQL 16 测试库中的 Trojan TLS 代理验收通过：临时 proxy 节点及共享单跳线路下发配置版本 1；Trojan TCP CONNECT 到服务器 HTTP ready 接口得到 200；用量账本上传 79、下载 191、计费 270 字节。临时 API 容器、测试库、Agent 凭据和脚本已删除，正式库仍为 0 用户、0 节点、0 Agent。
- 使用 Clash Verge 内置 Mihomo Meta `v1.19.31` 原生 `-t` 校验生成的 Clash 和 Mihomo YAML，两个配置均通过；新增可选 `CONTROL_TEST_MIHOMO_BINARY` 回归测试，未设置时跳过，不影响普通开发环境。
- 下载并按 GitHub 官方 SHA-256 校验 sing-box `v1.12.0` macOS arm64 发布包；用原生 `sing-box check` 验证默认、分流和阻断 fallback 三种生成 JSON，全部通过。新增可选 `CONTROL_TEST_SING_BOX_BINARY` 回归测试。
- 修复线路管理页面的权限路径缺口：管理员现在可以在共享线路卡片上启停 `/api/v1/admin/lines/{id}`，普通用户仍只能启停自己的 `/api/v1/lines/{id}`；新增路径选择回归测试。前端 21 项测试和生产构建通过。
- 已通过 Termark 将 `d14fdbb` 发布到 `us dmit` 的 `/opt/network-control-plane/releases/release-d14fdbb`。发布包 SHA-256 `c44d0bcbde8a3859b021e236cfa954c5b19bb2039eaa8e0ebff0b9d37d0f1094`，升级前正式库备份 `/opt/network-control-plane/backups/ncp-before-d14fdbb.dump` 权限 0600。初次 `docker compose up` 遗漏既有 Agent TLS 覆盖文件，随即用 `compose.agent-tls.yaml` 和原证书目录重建恢复；最终 API/DB healthy、API 重启 0，18443 仅绑定 `127.0.0.1`，公网首页/ready 200、登录 404，迁移 16，正式库用户/节点/Agent 均为 0。临时上传包已删除，磁盘剩余约 1.4GB。

## 2026-09-27 自定义 RBAC 发布

- 提交 `f2c21db`：自定义角色的权限目录、创建/更新/删除，普通用户角色分配、审计、REST/OpenAPI、管理 UI 与现有会话权限即时生效。系统角色不可编辑，角色写操作限定系统管理员，自定义角色不得包含 `roles.write`。审查修复了大小写 UUID 自我分配绕过、角色权限自我提升路径，以及角色分配/删除并发时的错误处理。
- 本地完整 Go 测试、Go vet、前端 26 项测试、TypeScript/生产构建、OpenAPI YAML 解析与 diff 检查通过。服务器独立 PostgreSQL 16 测试库完成迁移 1–18 和角色生命周期测试，随后删除临时库。
- 经 Termark 发布到 `us dmit` 的 `/opt/network-control-plane/releases/release-f2c21db`，发布包 SHA-256 `b511e1e3692a035ede8ff81c27b7ddf30db96ac11f00a48a8c55f7c735a44039`。正式库升级前备份 `ncp-before-f2c21db.dump` 权限 0600，迁移版本 18。API/DB healthy、重启 0；公网首页/ready 200，明文登录和未认证角色 API 404；Agent TLS 18443 仍仅绑定回环。正式库仍为 0 用户、0 节点、0 Agent；磁盘剩余约 1.2 GB。
- 继续开发可执行多跳与线路权重切换。正式管理员登录及生产节点验收仍需受信任 HTTPS 管理入口和首个正式节点位置。

## 2026-09-27 RBAC 前端权限边界修复

- 提交 `c6bd719`：管理 UI 隐藏保留给系统管理员的 `roles.write` 权限，并同步 OpenAPI 说明；新增前端回归测试。
- 前端 27 项测试、TypeScript/生产构建、OpenAPI YAML 解析和 diff 检查通过。经 Termark 发布到 `/opt/network-control-plane/releases/release-c6bd719`，包 SHA-256 `ed908a1d00f6d45086679281779b28fe20d12502cafe87ca4ac856f0c9005bc8`；升级前正式库备份 `ncp-before-c6bd719.dump` 权限 0600，迁移保持 18。API/DB 最终 healthy、重启 0，公网首页/ready 200、登录 404，18443 仅绑定回环；正式库仍无用户、节点、Agent。

## 2026-09-27 管理员审计查看发布

- 提交 `24bba90`：增加 `audit.read` 保护的审计分页 API 和管理页，只返回操作元数据，不查询或返回前后状态快照；迁移 19 增加全局时间与 ID 排序索引。独立 PostgreSQL 16 测试库完成迁移 1–18 后的审计分页集成测试，另一个临时库验证索引 up/down；临时库均已清理。
- 全量 Go 测试、Go vet、前端 27 项测试、TypeScript/生产构建、OpenAPI YAML 解析通过。服务器包 SHA-256 `ce2ce683d9e0f08a8bd4a9fea4b2848578e0428c4b10136bcacd46cc6f0112a8`；升级前正式库备份 `ncp-before-24bba90.dump` 权限 0600，并经 PostgreSQL 16.10 `pg_restore --list` 验证可读取。发布后 API/DB healthy、重启 0，迁移版本 19、索引存在；回环首页/live/ready 为 200，明文登录和审计接口为 404，Agent TLS 18443 仅绑定回环。正式库用户、节点和 Agent 均为 0。
- 仍需首个正式管理员、受信任 HTTPS 管理入口、正式 Agent 入网与真实数据面验收；可执行多跳与线路权重尚未实现。

## 2026-09-27 多跳拓扑草稿

- 2–8 跳拓扑支持连续位置、唯一节点、入口/中转/出口角色校验；仅接受显式停用草稿。创建时检查全部节点能力、启用状态、资源组授权、套餐跳数与自有线路数；禁止启用多跳，既有代理、订阅和计费路径仍要求单跳。
- 前端增加多跳草稿表单与角色展示，套餐可设置 1–8 跳。独立加权排序器通过优先级、健康过滤、3:1 权重分布与稳定重试测试，尚未接入实际运行时。
- 全量 Go 测试（允许回环监听）、Go vet、前端 28 项测试、TypeScript/生产构建、OpenAPI YAML 解析与 diff 检查通过。独立 PostgreSQL 16 库全部 5 项资源目录集成测试通过，临时数据库和三个测试二进制已清理。
- 实际 Agent 间中继、多节点配置世代、TCP/UDP 多跳通流和权重切换尚未实现。本切片无新增 migration，正式库保持版本 19。已通过 Termark 发布提交 `3454b70` 到 `/opt/network-control-plane/releases/release-3454b70`，发布包 SHA-256 `95ecab9d6c9c0667d71e1102295cd6eef562fb2ecb6792e1013c765f2d04f0f6`；正式库备份 `ncp-before-3454b70.dump` 权限 0600，已通过 `pg_restore --list` 验证。发布后 API/DB healthy、重启 0，首页/ready 200、明文登录 404，Agent TLS 18443 仅绑定回环；正式库用户/节点/Agent 均为 0。

## 2026-09-27 Agent 中继握手协议基础

- 新增独立 `internal/agentrelay` 包：4 字节长度前缀、4 KiB 帧限制、严格 JSON 字段、`OPEN`/`OPEN_OK`/`OPEN_ERR`、线路世代和目标校验、每条线路 32 字节密钥的 HMAC 证明、并发安全且容量有界的重放窗口。TLS 来源证书绑定与公网 DNS 解析仍属于后续运行时，协议尚未接入 Agent。
- 测试覆盖修改目标后证明失效、旧世代、时钟窗口边界、重复并发握手、私网 IP、超长帧、未知/重复/大小写别名字段以及握手后数据流完整。全量 Go 测试、Go vet、race 测试及 3 秒 fuzz 测试通过；该切片未改变已部署服务。

## 2026-09-27 节点中继端口预留

- 迁移 20 增加节点可选 `relay_port`、节点主机 TCP 端点统一唯一约束和 `port_allocations` 的中继占用类型。设置端口时原子预留 TCP/UDP，停用节点仍保留端口，转发仓储拒绝占用。管理员创建节点表单、OpenAPI 和中继协议文档已同步。
- 独立 PostgreSQL 16 临时库验证迁移前红测试、升级后全部目录和转发集成测试、降级再升级及再次测试；临时库已删除。测试中临时中继预留与后续取消 forward 能力的场景冲突，已在场景结束释放测试预留。全量 Go 测试、Go vet、前端 29 项测试、TypeScript/构建及 OpenAPI YAML 解析通过。
- 本轮未部署中继端口版本，正式服务仍为 `3454b70`、迁移 19。端口字段只完成资源预留；中继握手包尚未接入 TLS 监听、配置编译或代理连接。传输方式仍待确认。

## 2026-09-27 中继服务端证书与双向 TLS 基础

- `agentidentity` 新增中继专用 ServerAuth 证书签发：CSR 身份/SAN 被忽略，节点 URI 与登记地址由控制面指定；现有 Agent ClientAuth 证书不能冒充服务端。
- `agentrelay` 新增 TLS 1.3 双向配置：验证 CA、DNS/IP SAN、节点 URI、证书指纹及来源节点在线授权回调。真实 TLS 握手测试覆盖正确身份、错误节点、错误 SAN、错误指纹及被拒绝来源。测试发现调用方可修改允许指纹切片，已复制为配置快照并加回归测试。
- 全量 Go 测试、Go vet、身份和中继包 race 测试通过。HTTP 签发接口、Agent 证书持久化与实际中继监听仍未实现；本次代码暂不部署。

## 2026-09-27 中继端口版本发布

- 通过 Termark 发布 `c0af258` 到 `/opt/network-control-plane/releases/release-c0af258`，包 SHA-256 `80be7f2d40cfb8d96fed8fd82792668d05d3dfad48b7077e82ae68ead5f899f0`。正式库升级至迁移 20，升级前备份 `ncp-before-c0af258.dump` 权限 0600，已通过 `pg_restore --list` 验证。
- 发布后 API/DB healthy、重启 0，首页/ready 200，明文登录 404，Agent TLS 18443 只绑定回环；正式库用户/节点/Agent 均为 0，磁盘可用约 2.4 GB。现有 Nginx Proxy Manager 保持运行。后续提交 `f6edf9b` 的 TLS 基础尚未部署。

## 2026-09-27 独立三节点 TCP 中继执行器

- `internal/agentrelay` 增加出口/中转处理器和出口拨号入口：TLS 对端认证后校验上一跳、线路世代、HMAC 证明和重放窗口；中转只使用已配置下一跳建立新 TLS 并重签 OPEN，出口重新验证公网目标 IP。下游 ACK 后才向上游发送成功；路线撤销关闭整链连接。
- 使用真实回环 TCP、三节点证书与独立 TLS 会话验证入口→中转→出口字节往返和撤销；测试覆盖私网解析拒绝。TLS 关闭在对端不读关闭通知时曾等待约 5 秒，新增失败测试并改为直接关闭底层连接，随后测试通过。
- 全量 Go 测试、Go vet、`agentrelay`/`agentidentity` race 测试通过。处理器尚未接入控制面配置快照、Agent 进程、计费或服务器数据面，不能把多跳草稿标为可用。

- 2026-09-27：新增迁移 21 和受 Agent mTLS 保护的 `/api/v1/agent/relay-certificate`。控制面在节点、资源组和 Agent 行锁内验证启用状态、forward 能力、relay_port、当前证书指纹和证书授权；SAN 只能来自节点登记 host。中继证书记录支持幂等重试、有限重叠续期和节点/资源变更触发撤销，重新入网会清除旧授权。
- 2026-09-27：Agent 侧新增独立 Ed25519 中继密钥生成、0600 原子保存、CSR 请求和返回证书的 CA、节点 URI、SAN、指纹及密钥匹配校验。中继证书尚未接入 Agent 主循环和线路快照下发；生产多跳仍未开放。
- 2026-09-27：relay 配置已进入 Agent 快照类型、严格解码与 canonical digest，支持 ingress/relay/egress 角色和相邻边密钥；能力门槛识别 `relay`。运行时监听器、线路世代编译和 Agent 启动续期尚未接线。

## 2026-09-27 多跳 Agent 运行时开发中

- 已将独立中继证书申请与续期接入可选 Agent 启动配置；已实现中继 TLS 监听、上一跳证书指纹校验和入口代理经固定下一跳建立 TCP 连接。`ProxyAccess.relay_generation` 必须与同一快照中的入口线路世代匹配，缺少线路时拒绝配置，避免回退为直连。
- 本地三运行时测试完成入口→中转→出口 TCP 往返、入口唯一计量和中转撤销关闭连接；全量 Go 测试、关键运行时 race 测试、Go vet 与 diff 检查通过。
- 多跳线路仍只可保存停用草稿。数据库线路拓扑读取、正式配置收敛、全跳 ACK 后启用代理、加权线路选择、迁移 22 的 PostgreSQL 验证及实际节点部署尚未完成。本轮没有更新 `us dmit`；服务器仍为 `release-c0af258`、正式迁移 20，公网纯 IP 入口仍只读。
