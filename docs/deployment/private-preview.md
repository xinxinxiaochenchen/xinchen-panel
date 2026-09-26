# 纯 IP 预览部署（`us dmit`）

本部署包含节点、单跳线路、转发、代理连接、订阅、分流、账期、用量账本、额度租约、周期 worker，以及管理员和账户页面。公网仍是纯 IP 只读预览；浏览器认证与 Agent TLS 入口未启用，真实代理流量尚未在服务器验收。

## 当前状态（2026-09-26）

- 已在 `us dmit` 部署提交 `5c297c2`，目录 `/opt/network-control-plane/releases/release-5c297c2`。当前版本包含 Surge 订阅导出、前端格式切换和管理员转发目标策略页面。
- PostgreSQL 16.10 容器健康；API 映射 `0.0.0.0:18080`，供纯 IP 测试。
- 从开发机请求 `http://179.255.145.149:18080/` 返回 200 HTML，浏览器渲染七项导航、浅深色主题、手机布局及真实就绪状态；`/api/v1/health/live` 与 `/ready` 均返回 200 `{"status":"ok"}`。
- 迁移记录版本 1–15；本次升级前备份 `/opt/network-control-plane/backups/ncp-before-5c297c2.dump`，权限 `0600`。
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

## 布局与边界

- 服务器：Termark 资产 `us dmit`，Debian 12 x86_64，Docker Compose v5。
- 部署目录：`/opt/network-control-plane`。Compose 项目名 `network-control-plane`，不会管理已有的 1Panel 和 Nginx Proxy Manager 容器。
- PostgreSQL 只在 Compose 私有网络中提供服务，不映射主机端口。
- API 默认映射到主机 `127.0.0.1:18080`。临时纯 IP 预览可在 `.env` 中设 `CONTROL_BIND_IP=0.0.0.0`，通过 `http://179.255.145.149:18080` 访问健康接口。80/443 仍由现有 Nginx Proxy Manager 使用。
- 服务器内存约 1 GiB；数据库和 API 分别设 256 MiB、128 MiB 容器限制。部署后观察内存和重启次数。

## 部署

1. 在开发机用 `GO_BIN=/path/to/go sh scripts/build-linux-amd64.sh` 构建 React 静态资源及 Linux amd64 的 `bin/control-plane`、`bin/migrate`、`bin/admin-bootstrap`、`bin/agent` 和 `bin/agent-token`。将 `apps/web/dist`、二进制、迁移和 Compose 文件打包，通过 Termark 上传到 `/opt/network-control-plane/release-<commit>.tar.gz` 并解压到独立 release 目录。服务器只需从预编译文件构建小镜像。上传前确认包不含 `.git` 或 `.env`。
2. 在 `deployments/compose/.env` 写入随机的 URL 安全数据库密码，文件权限设为 `0600`；不要把密码写进 Git 或终端输出。
3. 在 `deployments/compose` 运行 `docker compose config --quiet`，确认配置可解析，并根据 `CONTROL_BIND_IP` 核对实际映射地址。临时纯 IP 预览设置 `CONTROL_BIND_IP=0.0.0.0`。
4. 运行 `docker compose up -d --build`。预编译镜像从 `scratch` 构建；Compose 等 PostgreSQL 健康后执行一次 `migrate up`，成功后启动 API。
5. 运行 `docker compose ps`、`docker compose logs --tail=50 migrate`；从公网纯 IP 请求 `/` 应得到 HTML，`/api/v1/health/live` 与 `/api/v1/health/ready` 应得到 200 `{"status":"ok"}`，`POST /api/v1/auth/login` 应保持 404。检查 `schema_migrations` 记录与表数量，并确认现有 80/443 容器仍在。

## 回滚与数据

在部署目录运行 `docker compose down` 可停止本项目，保留 `postgres_data` 卷。不要运行 `down -v`。升级前备份数据库；如果需要回退镜像和源码，恢复上一个 release 目录并重新执行 `docker compose up -d --build`。已应用的数据库 migration 默认不可逆，含迁移的版本回退前需单独审查 SQL 与数据兼容性。

## 公网接入

当前按用户要求开放 18080 用于纯 IP 页面与连通性测试；页面只显示预览内容，登录后的操作页尚未对公网开放。不要在明文 HTTP 上测试带 Cookie 的登录功能。域名和证书就绪后，在现有 Nginx Proxy Manager 中创建 HTTPS 代理主机，并将 `CONTROL_BIND_IP` 恢复为 `127.0.0.1`，再关闭公网 18080 访问。
