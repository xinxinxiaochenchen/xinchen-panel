# `us bwg` 纯 IP 预览

2026-09-27 发布提交 `e69c44d` 到 Termark 资产 `us bwg`。服务器地址 `144.34.238.89`，预览入口为 `http://144.34.238.89:18080/`。这是只读页面与健康检查入口；浏览器身份路由关闭，正式库没有用户、节点或 Agent，多跳线路仍只可保存为停用草稿。

## 部署布局

- 发布目录：`/opt/network-control-plane/releases/release-e69c44d`
- Compose 文件：`deployments/compose/compose.yaml`
- Compose 项目：`network-control-plane`
- PostgreSQL：`postgres:16.10-bookworm`，仅接入项目私有网络，不映射主机端口
- 控制面：主机 `0.0.0.0:18080` 映射到容器 `8080`
- 环境文件：发布目录下的 `deployments/compose/.env`，权限 `0600`；数据库密码在服务器本地随机生成，不在发布包中
- 发布包 SHA-256：`6a9566331a7b69d4c52e8b4211e91652e9cb6ee1c60b2f2a16f15367c0849e91`

本次只使用基础 Compose 文件。`compose.agent-tls.yaml`、`compose.agent-local.yaml` 和 `compose.relay-secrets.yaml` 未启用。没有创建正式管理员或 Agent 证书，也没有更改现有 Nginx Proxy Manager。

## 验收记录

- Linux amd64 二进制及前端生产资源在开发机生成；完整 `go test ./...` 通过。
- 发布包在服务器上复核 SHA-256 一致；`docker compose config --quiet` 通过。
- 真实 PostgreSQL 16.10 执行 migration 至版本 22。
- API、数据库容器均为 `healthy`，重启计数为 0。
- 从开发机请求公网首页与 `/api/v1/health/ready` 均为 200；`POST /api/v1/auth/login` 为 404。
- 正式库用户、节点、Agent 数均为 0；已有 80/81/443 和 8787 容器继续运行。

## 维护命令

通过 Termark 连接 `us bwg` 后，在 `/opt/network-control-plane/releases/release-e69c44d/deployments/compose` 目录执行 `docker compose ps`、`docker compose logs --tail=50 api` 查看状态。后续升级前先备份数据库并校验备份，再发布新目录；不要删除 `network-control-plane_postgres_data` 数据卷。服务器临时上传包可在确认发布状态后清理。

后续若启用真实用户和节点，需要先准备受信任 HTTPS 管理入口、管理员初始化、Agent mTLS 入网与端到端通流验收。公网 18080 明文预览不承载登录或订阅 Token。
