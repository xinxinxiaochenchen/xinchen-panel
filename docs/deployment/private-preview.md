# 纯 IP 预览部署（`us dmit`）

本部署包含节点、单跳线路、转发、代理连接、订阅、分流、账期、用量账本、额度租约、周期 worker，以及管理员和账户页面。公网仍是纯 IP 只读预览；浏览器认证关闭，Agent TLS 仅在服务器回环地址开放，真实代理流量尚未在服务器验收。

## 当前状态（2026-09-27）

- 已发布 `release-ipv6-cidr-20260927`：Mihomo 订阅对 IPv6 地址及受控 GeoIP IPv6 网段生成 `IP-CIDR6`。发布包 SHA-256 `c011de4926a43ca0a7538b820d90c13d43dfeecfe0e43d1e7bea48c63621b13f`；升级前备份 `/opt/network-control-plane/backups/ncp-before-ipv6-cidr-20260927.dump` 权限 0600。API/DB healthy、重启 0，回环首页/live/ready 200，明文登录和未认证规则集接口 404，迁移版本 17。清理已解压的旧上传归档后，磁盘可用空间约 1.5 GB；发布目录和数据库备份保留。正式库仍无用户、节点或 Agent。
- 已发布 `3d4faf4` 至 `/opt/network-control-plane/releases/release-3d4faf4`，新增管理员上传、版本化和启停 GeoSite/GeoIP 规则集，并在订阅导出时按同一数据库快照展开。发布包 SHA-256 `0a49dd6ed8262cbe3de4f1e7a79be6dc85199110077f70b92fea1416bb9104b8`；升级前正式库备份 `/opt/network-control-plane/backups/ncp-before-3d4faf4.dump` 权限 0600。迁移版本 17，API/DB healthy；公网 IP 首页/ready 200，明文登录 404，未认证规则集 API 404；现有 Nginx Proxy Manager 未改动。正式库仍为 0 用户、0 节点、0 Agent，规则集 0 条。
- 已发布 `1852394` 至 `/opt/network-control-plane/releases/release-1852394`，补齐目录中共享线路与自有线路的编辑入口：管理员和线路所有者分别使用受限 PATCH 路径，编辑名称、优先级、权重、标签；共享线路可调整倍率。上传的发布包 SHA-256 `d5abd8dedd82bbf04180fadcae442709213338aaba3e9425560e1cb27c3fe6b8`；升级前正式库备份 `/opt/network-control-plane/backups/ncp-before-1852394.dump` 权限 0600。服务器 API/DB healthy，迁移版本 16；公网 IP 首页/ready 200，明文登录 404；现有 Nginx Proxy Manager 未改动。正式库仍为 0 用户、0 节点、0 Agent。
- 已发布 `34f44bc` 至 `/opt/network-control-plane/releases/release-34f44bc`，新增管理员停用/恢复普通用户、会话撤销、审计和 Agent 配置收敛。发布包 SHA-256 `8ccc8bbe1ce891fe2f5c64fed4e1bd44a8a93ff180cd683e20ba9e9609861580`；升级前正式库备份 `/opt/network-control-plane/backups/ncp-before-34f44bc.dump` 权限 0600。迁移版本 16，API/DB healthy、重启 0；公网 IP 首页/live/ready 200、明文登录 404，回环 mTLS 续签无证书 401。正式库仍为 0 用户、0 节点、0 Agent。
- 已发布 `72d9c73` 至 `/opt/network-control-plane/releases/release-72d9c73`，新增 Agent 证书在线交接。发布包 SHA-256 为 `539a91746336e77e7ef3cb518537c7467fa5ecafed37cf16df1ff5290fa0a9d6`；升级前备份 `/opt/network-control-plane/backups/ncp-before-72d9c73.dump` 权限 0600。API/DB 均 healthy，API 重启 0，迁移保持 16。服务器回环首页和 ready 为 200，HTTP 登录为 404，无证书 mTLS 续签为 401。正式库用户、节点、Agent 仍均为 0。Agent overlay 未启动，真实节点会话仍待验收。
- 已发布 `0313ce5` 至 `/opt/network-control-plane/releases/release-0313ce5`，增加 Agent 证书自动续签和迁移 16。发布包 SHA-256 为 `433132344f74c2ede7c6f9573d62f698720e56c404e573a07536c21dc1169219`；升级前正式库备份 `/opt/network-control-plane/backups/ncp-before-0313ce5.dump` 权限 0600。独立 PostgreSQL 16 测试库通过 Agent 身份全套集成测试及迁移 16 down/up，测试库已清理。正式 API/DB healthy、API 重启 0，迁移 16；公网首页和 ready 为 200，明文登录与 Agent 续签均为 404；Agent TLS 回环无证书续签返回 401。正式库用户、节点、Agent 仍均为 0，Agent overlay 未启动。后续开发的在线证书交接尚未包含在此版本。
- 已在 `us dmit` 部署提交 `15f7a55`，目录 `/opt/network-control-plane/releases/release-15f7a55`。当前版本增加受限的本机节点引导命令，可事务化创建资源组与节点，重复执行返回同一节点。
- PostgreSQL 16.10 容器健康；API 映射 `0.0.0.0:18080`，供纯 IP 测试。
- 从开发机请求 `http://179.255.145.149:18080/` 返回 200 HTML，浏览器渲染七项导航、浅深色主题、手机布局及真实就绪状态；`/api/v1/health/live` 与 `/ready` 均返回 200 `{"status":"ok"}`。
- 迁移记录版本 1–15；本次升级前备份 `/opt/network-control-plane/backups/ncp-before-15f7a55.dump`，权限 `0600`。
- 运行容器中 `CONTROL_BROWSER_AUTH_ENABLED=false`（Compose 默认值），浏览器认证关闭。公网 `/api/v1/auth/login` 返回 404；不能在明文 HTTP 上提交密码。
- 部署时发现 macOS AppleDouble 元数据文件影响迁移发现，已加入回归测试和加载器过滤，见提交 `2594ba4`。原始失败的 `0695de1` 发布目录留作排障记录，未作为当前运行版本。
- 现有 Nginx Proxy Manager 容器及 80/443 端口未改动。预览实例未配置公网域名或反向代理。
- 2026-09-26 发布 `a056e72`：公网首页 HTML、就绪探针均返回 200，登录返回 404；浏览器预览页实时状态显示“运行正常”。API/DB 容器均为 healthy、重启次数为 0；现有 Nginx Proxy Manager 未改动。迁移 5 已先在独立库验证，测试库及临时文件已清理。
- 2026-09-26 发布 `ce02780`：控制面新增配置收敛 worker。正式库升级前备份为 `/opt/network-control-plane/backups/pre-convergence-ce02780.dump`，权限 `0600`；迁移仍为 5。公网首页和就绪探针返回 200，登录返回 404。API/DB 均 healthy、重启次数 0；正式库暂无 Agent，因而尚无实际转发。独立测试数据库和测试二进制已清理，原有 Nginx Proxy Manager 继续运行。
- 2026-09-26 发布 `74fc614`：Agent 入网身份基础、审计和限流已纳入镜像，但相关 TLS/浏览器入口仍关闭。迁移 6 在独立 PostgreSQL 16 测试库通过 up/down；正式库升级前备份为 `/opt/network-control-plane/backups/pre-agent-74fc614.dump`，权限 `0600`。正式库迁移至版本 6，Agent 数为 0。公网纯 IP 首页及就绪接口返回 200，登录与 Agent 入网入口返回 404；实际 Agent 通道和转发仍未部署。
- 2026-09-26 发布 `231744f`：Agent mTLS WebSocket、配置快照与回执、心跳指标及本机 Agent 二进制纳入发布包。迁移 7 在独立 PostgreSQL 16 测试库通过 up/down 和指标集成测试；正式库升级前备份为 `/opt/network-control-plane/backups/pre-agent-stream-231744f.dump`，权限 `0600`。正式库已迁移至版本 7，Agent 数为 0。公网 `http://179.255.145.149:18080/` 首页和就绪接口返回 200，登录、入网和配置流返回 404。Agent TLS 未配置，实际转发尚未在服务器运行；Nginx Proxy Manager 未改动。
- 2026-09-26 发布 `e4b5e56`：代理连接 REST、Trojan TLS Agent 执行、配置租约、能力协商与迁移 8/9 已纳入镜像。独立 PostgreSQL 16 测试库通过迁移 8/9 up/down 和代理版本生命周期；正式库升级前备份为 `/opt/network-control-plane/backups/pre-proxy-e4b5e56.dump`，权限 `0600`。正式库已迁移至版本 9，Agent 数为 0。公网首页与健康探针 200，登录和 Agent 入网 404；API/DB healthy，API 重启次数 0。当前尚无流量额度执行、订阅和真实 Agent，因此代理服务未开放。
- 2026-09-26 发布 `a40cd29`：Mihomo/sing-box 订阅生成、Token 管理、当前授权与已应用凭据过滤、REST/OpenAPI 及迁移 10 纳入镜像。独立 PostgreSQL 16 测试库通过迁移 10 up/down/up 与生命周期、并发限额、轮换和撤销测试；正式库升级前备份 `pre-subscriptions-a40cd29.dump` 权限 0600。正式库升级至版本 10，Agent 数 0，API/DB healthy、API 重启次数 0。公网首页与健康接口 200，登录/订阅管理/公开订阅及 Agent 入网入口 404；订阅路径 404 响应 no-store。80/443 的现有 Nginx Proxy Manager 未变。真实代理、流量额度、分流和可操作控制台仍待完成。

- 2026-09-26 发布 `2e87597`：账期、用量账本、计费字节租约与周期 worker 纳入镜像。正式库先备份，随后应用迁移 11/12；Agent 数 0，API/DB healthy，重启次数 0。公网首页与 live/ready 200，登录、订阅管理、公开订阅及 Agent 入网 404；公开订阅路径返回 no-store。现有 Nginx Proxy Manager 80/443 未改动。Agent 上报与本地额度执行、分流、可操作前端及真实节点验收仍待完成。

- 2026-09-26 发布 `011d97a`：管理员可启停节点；事务内写审计和 outbox，节点状态变化重算全部已入网 Agent 的配置，以撤销指向停用目标节点的其他入口转发。完整 Go 测试、vet、前端 20 项测试、类型检查、生产构建与 OpenAPI 解析通过。发布包 SHA-256 为 `24347f9861ccf3b68daf04c42ce4acb8a7744378888b5cbb863308c702a43fcd`，正式库升级前备份权限 0600。公网 IP 首页、live、ready 均 200，登录 404；API/DB healthy、重启次数 0；迁移版本 15，节点和 Agent 数均为 0。

- 2026-09-26 发布 `1f98d29`：增加显式 Agent TLS IP 监听开关和可选 Compose 覆盖配置。发布包 SHA-256 `192a79387a8cb1d7534b43ba0a5605182964f34304a74f5db17ac96a8db40af2`，升级前正式库备份 `ncp-before-1f98d29.dump` 为 0600。服务器生成专用 Ed25519 CA 与服务器证书，证书匹配回环和公网 IP，私钥权限 0600；证书有效至 2027-09-26。覆盖配置仅将 18443 映射到主机 `127.0.0.1`；可信 CA 的 TLS 请求到 Agent 入网路径返回 405，证明握手和路由可用。公网首页/live/ready 200、登录 404，API/DB healthy、重启 0，迁移 15，节点/Agent 仍为 0。
- 2026-09-27 发布 `15f7a55`：新增本机 `node-bootstrap`。发布包 SHA-256 `43c3a7427bc61f221f03f74223cb765a5838d34704c3583ddedbdd23d815ae2c`；正式库升级前备份 `/opt/network-control-plane/backups/ncp-before-15f7a55.dump`，权限 0600。独立 PostgreSQL 16 测试库通过幂等、禁用资源组拒绝和审计失败回滚测试，测试库已删除。正式 API/DB healthy、API 重启 0，迁移 15；公网首页/live/ready 为 200，登录 404；Agent TLS 回环入网路径返回 405。正式库用户、资源组、节点、Agent 均为 0，尚未进行真实入网或数据面验收。
- 2026-09-27 发布 `4b5b657` 与 `34df875`：订阅增加经典 Clash YAML 格式，完成 GeoIP/IPv6 CIDR、线路选择和规则注入边界测试；随后加入可选同机 Agent Compose overlay。最终发布包 SHA-256 `d7bd61a4645a476044f49330c21e0b4835ada284048ddfc455253622bfe1bbcb`，正式库备份 `/opt/network-control-plane/backups/ncp-before-34df875.dump`，权限 0600。服务器 API/DB healthy、重启 0，迁移 15，公网首页/live/ready 200，登录 404；Agent overlay 仅完成 Compose 解析验证，未启动 Agent，正式库仍无用户、节点和 Agent。

## 布局与边界

- 服务器：Termark 资产 `us dmit`，Debian 12 x86_64，Docker Compose v5。
- 部署目录：`/opt/network-control-plane`。Compose 项目名 `network-control-plane`，不会管理已有的 1Panel 和 Nginx Proxy Manager 容器。
- PostgreSQL 只在 Compose 私有网络中提供服务，不映射主机端口。
- API 默认映射到主机 `127.0.0.1:18080`。临时纯 IP 预览可在 `.env` 中设 `CONTROL_BIND_IP=0.0.0.0`，通过 `http://179.255.145.149:18080` 访问健康接口。80/443 仍由现有 Nginx Proxy Manager 使用。
- 服务器内存约 1 GiB；数据库和 API 分别设 256 MiB、128 MiB 容器限制。部署后观察内存和重启次数。

## 部署

1. 在开发机用 `GO_BIN=/path/to/go sh scripts/build-linux-amd64.sh` 构建 React 静态资源及 Linux amd64 的 `bin/control-plane`、`bin/migrate`、`bin/admin-bootstrap`、`bin/node-bootstrap`、`bin/agent` 和 `bin/agent-token`。将 `apps/web/dist`、二进制、迁移、基础 Compose 文件及可选 `compose.agent-tls.yaml` 打包，通过 Termark 上传到 `/opt/network-control-plane/release-<commit>.tar.gz` 并解压到独立 release 目录。服务器只需从预编译文件构建小镜像。上传前确认包不含 `.git` 或 `.env`。
2. 在 `deployments/compose/.env` 写入随机的 URL 安全数据库密码，文件权限设为 `0600`；不要把密码写进 Git 或终端输出。
3. 在 `deployments/compose` 运行 `docker compose config --quiet`，确认配置可解析，并根据 `CONTROL_BIND_IP` 核对实际映射地址。临时纯 IP 预览设置 `CONTROL_BIND_IP=0.0.0.0`。
4. 运行 `docker compose up -d --build`。预编译镜像从 `scratch` 构建；Compose 等 PostgreSQL 健康后执行一次 `migrate up`，成功后启动 API。
5. 运行 `docker compose ps`、`docker compose logs --tail=50 migrate`；从公网纯 IP 请求 `/` 应得到 HTML，`/api/v1/health/live` 与 `/api/v1/health/ready` 应得到 200 `{"status":"ok"}`，`POST /api/v1/auth/login` 应保持 404。检查 `schema_migrations` 记录与表数量，并确认现有 80/443 容器仍在。

## 回滚与数据

在部署目录运行 `docker compose down` 可停止本项目，保留 `postgres_data` 卷。不要运行 `down -v`。升级前备份数据库；如果需要回退镜像和源码，恢复上一个 release 目录并重新执行 `docker compose up -d --build`。已应用的数据库 migration 默认不可逆，含迁移的版本回退前需单独审查 SQL 与数据兼容性。

## 公网接入

当前按用户要求开放 18080 用于纯 IP 页面与连通性测试；页面只显示预览内容，登录后的操作页尚未对公网开放。不要在明文 HTTP 上测试带 Cookie 的登录功能。域名和证书就绪后，在现有 Nginx Proxy Manager 中创建 HTTPS 代理主机，并将 `CONTROL_BIND_IP` 恢复为 `127.0.0.1`，再关闭公网 18080 访问。

## Agent TLS 纯 IP 接入（回环已启用，公网未开放）

Agent 使用独立的 HTTPS/mTLS 入口，与浏览器的 HTTP 预览端口分开。启用前准备一组专用 CA 和服务器证书：服务器证书的 IP SAN 必须覆盖 Agent URL 所用的 IP；Agent 预装该 CA 证书并保持证书校验开启。CA 私钥和服务器私钥仅留在控制面服务器，不能复制到节点。

将 `server.crt`、`server.key`、`agent-ca.crt`、`agent-ca.key` 放入服务器上的绝对路径目录；目录建议由容器用户 UID 65532 拥有、权限 0700，两个私钥为 0600。可选覆盖文件 `deployments/compose/compose.agent-tls.yaml` 将此目录只读挂载到 API 容器，并启用 `CONTROL_AGENT_PUBLIC_TLS_ENABLED=true`。API 容器内监听 18443，主机默认仅绑定 `127.0.0.1:18443`。先在 release 的 `deployments/compose` 目录运行：

```sh
CONTROL_AGENT_TLS_DIR=/opt/network-control-plane/secrets/agent-tls \
  docker compose -f compose.yaml -f compose.agent-tls.yaml config --quiet
```

同机 Agent 可连接 `wss://127.0.0.1:18443/api/v1/agent/stream`，证书需含回环 IP SAN。若 Agent 在另一台服务器，先确保服务器证书含公网 IP SAN，再将 `CONTROL_AGENT_BIND_IP=0.0.0.0` 用于覆盖文件，并只允许该节点来源访问 18443。启动时同样必须提供证书目录变量：

```sh
CONTROL_AGENT_TLS_DIR=/opt/network-control-plane/secrets/agent-tls \
  docker compose -f compose.yaml -f compose.agent-tls.yaml up -d --build
```

不指定覆盖文件时，现有预览入口和 Agent TLS 关闭状态保持原样。此覆盖文件只开放 Agent 通道，不开放管理接口；当前纯 IP 预览关闭了浏览器身份路由，且正式库中没有节点。首个节点必须先通过受信任 HTTPS 管理入口创建，或使用后续提供的受限本机引导命令；随后管理员可发一次性令牌并让 Agent 用可信 CA 完成证书登记。仅启用此覆盖文件不能完成入网。

## 同机 Agent Compose overlay

发布包还包含 `compose.agent-local.yaml` 和 `Dockerfile.agent`。它们默认不参与启动；只有在节点已经创建、Agent 证书已登记、目录中存在 `agent-ca.crt`、`agent.crt`、`agent.key` 且权限正确时才启用。overlay 使用 host network，让 Agent 连接 `wss://127.0.0.1:18443/api/v1/agent/stream` 并在服务器上绑定节点端口；状态目录保存配置快照、额度租约和用量 outbox，容器本身保持只读文件系统。凭据目录需由 UID 65532 拥有并可写，供 Agent 在证书到期前六小时自动原子替换 `agent.crt`；`agent.key` 仍为 0600 且不轮换。

```sh
install -d -m 0700 /opt/network-control-plane/secrets/agent-credentials
install -d -m 0700 /opt/network-control-plane/state/agent
chown 65532:65532 /opt/network-control-plane/secrets/agent-credentials /opt/network-control-plane/state/agent
# 将控制面签发的 agent-ca.crt、agent.crt、agent.key 放入 credentials 目录；三个文件由 UID 65532 拥有，agent.key 为 0600
CONTROL_AGENT_TLS_DIR=/opt/network-control-plane/secrets/agent-tls \
CONTROL_AGENT_CREDENTIAL_DIR=/opt/network-control-plane/secrets/agent-credentials \
CONTROL_AGENT_STATE_DIR=/opt/network-control-plane/state/agent \
CONTROL_AGENT_NODE_ID=<node-uuid> \
  docker compose -f compose.yaml -f compose.agent-tls.yaml -f compose.agent-local.yaml up -d --build agent
```

如果节点具备 `proxy` 能力，还需要把独立的 `proxy.crt` 和 `proxy.key` 放入凭据目录，并通过 `CONTROL_AGENT_PROXY_CERT_FILE`、`CONTROL_AGENT_PROXY_KEY_FILE` 指定容器内路径；没有代理证书时，Agent 仍可运行转发能力，但会拒绝代理监听配置。停止时只停止 `agent` 服务，不要删除状态目录。
