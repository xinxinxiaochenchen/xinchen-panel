# `us bwg` 纯 IP 预览

2026-09-28 升级到代理候选池与管理员流量统计版本（发布目录见下方），经 Termark 资产 `us bwg` 部署。服务器地址 `144.34.238.89`，预览入口为 `http://144.34.238.89:18080/`。这是只读页面与健康检查入口；浏览器身份路由关闭，正式库没有用户、节点或 Agent。线路页显示单跳/多跳线路健康、每跳 Agent 在线状态、证书状态和配置应用进度。

## 部署布局

- 发布目录：`/opt/network-control-plane/releases/release-1ea63c9-20260928`
- Compose 文件：`deployments/compose/compose.yaml`
- Compose 项目：`network-control-plane`
- PostgreSQL：`postgres:16.10-bookworm`，仅接入项目私有网络，不映射主机端口
- 控制面：主机 `0.0.0.0:18080` 映射到容器 `8080`
- 环境文件：发布目录下的 `deployments/compose/.env`，权限 `0600`；数据库密码在服务器本地随机生成，不在发布包中
- 发布包 SHA-256：`e4cef22d760cdeb39164e9b0e5273a69027a44add454d8708e37b7f13eb7a0c1`

本次只使用基础 Compose 文件。`compose.agent-tls.yaml`、`compose.agent-local.yaml` 和 `compose.relay-secrets.yaml` 未启用。没有创建正式管理员或 Agent 证书，也没有更改现有 Nginx Proxy Manager。

## 验收记录

- Linux amd64 二进制及前端生产资源在开发机生成；发布前 Go 全量测试、`go vet ./...`、WebUI 37 项测试、生产构建和 OpenAPI 解析均通过。
- 服务器独立 PostgreSQL 16.10 临时库以全新数据库逐项运行 17 项代理、迁移、计费、多跳、转发、收敛和订阅集成回归，全部通过；临时数据库已删除。
- 正式 PostgreSQL 16.10 已迁移到版本 25。升级前备份为 `/opt/network-control-plane/backups/ncp-before-1ea63c9.dump`，权限 0600，并经容器内 `pg_restore --list` 校验。
- API、数据库容器均为 `healthy`，重启计数均为 0。
- 从服务器本机和公网 IP 请求首页、`/api/v1/health/live` 与 `/api/v1/health/ready` 均为 200；`POST /api/v1/auth/login` 为 404。
- 正式库用户、节点、Agent 数均为 0；已有 80/81/443 和 8787 容器继续运行。

## 维护命令

通过 Termark 连接 `us bwg` 后，在 `/opt/network-control-plane/releases/release-1ea63c9-20260928/deployments/compose` 目录执行 `docker compose ps`、`docker compose logs --tail=50 api` 查看状态。后续升级前先备份数据库并校验备份，再发布新目录；不要删除 `network-control-plane_postgres_data` 数据卷。

后续若启用真实用户和节点，需要先准备受信任 HTTPS 管理入口、管理员初始化、Agent mTLS 入网与端到端通流验收。公网 18080 明文预览不承载登录或订阅 Token。
