# 私有预览部署（`us dmit`）

本部署只验证控制面基础、PostgreSQL 迁移和健康接口。业务 API、前端和 Agent 尚未完成；公网 18080 仅供临时纯 IP 连通性测试，不是正式控制台。

## 当前状态（2026-09-26）

- 已在 `us dmit` 部署提交 `c5b1664`，目录 `/opt/network-control-plane/releases/c5b1664`。
- PostgreSQL 16.10 容器健康；API 映射 `0.0.0.0:18080`，供纯 IP 测试。
- 从开发机请求 `http://179.255.145.149:18080/api/v1/health/live` 与 `/ready` 均返回 200 `{"status":"ok"}`。
- 迁移记录版本 1、2；身份/RBAC 表和 21 个权限码已创建；重复执行 `migrate up` 成功。升级前备份位于 `/opt/network-control-plane/backups/pre-identity-c5b1664.dump`，权限 `0600`。
- 浏览器认证由 `CONTROL_BROWSER_AUTH_ENABLED=false` 关闭。公网 `POST /api/v1/auth/login` 返回 404；不能在明文 HTTP 上提交密码。
- 部署时发现 macOS AppleDouble 元数据文件影响迁移发现，已加入回归测试和加载器过滤，见提交 `2594ba4`。原始失败的 `0695de1` 发布目录留作排障记录，未作为当前运行版本。
- 现有 Nginx Proxy Manager 容器及 80/443 端口未改动。预览实例未配置公网域名或反向代理。

## 布局与边界

- 服务器：Termark 资产 `us dmit`，Debian 12 x86_64，Docker Compose v5。
- 部署目录：`/opt/network-control-plane`。Compose 项目名 `network-control-plane`，不会管理已有的 1Panel 和 Nginx Proxy Manager 容器。
- PostgreSQL 只在 Compose 私有网络中提供服务，不映射主机端口。
- API 默认映射到主机 `127.0.0.1:18080`。临时纯 IP 预览可在 `.env` 中设 `CONTROL_BIND_IP=0.0.0.0`，通过 `http://179.255.145.149:18080` 访问健康接口。80/443 仍由现有 Nginx Proxy Manager 使用。
- 服务器内存约 1 GiB；数据库和 API 分别设 256 MiB、128 MiB 容器限制。部署后观察内存和重启次数。

## 部署

1. 在开发机用 `scripts/build-linux-amd64.sh` 生成 `bin/control-plane` 与 `bin/migrate`。将这些静态 Linux 二进制、迁移和 Compose 文件打包，通过 Termark 上传到 `/opt/network-control-plane/release.tar.gz` 并解压。这样内存较小的服务器只需拉取 PostgreSQL 镜像，无需编译 Go。上传前确认没有覆盖别的应用，不要包含 `.git` 或 `.env`。
2. 在 `deployments/compose/.env` 写入随机的 URL 安全数据库密码，文件权限设为 `0600`；不要把密码写进 Git 或终端输出。
3. 在 `deployments/compose` 运行 `docker compose config --quiet`，确认配置可解析，并根据 `CONTROL_BIND_IP` 核对实际映射地址。临时纯 IP 预览设置 `CONTROL_BIND_IP=0.0.0.0`。
4. 运行 `docker compose up -d --build`。预编译镜像从 `scratch` 构建；Compose 等 PostgreSQL 健康后执行一次 `migrate up`，成功后启动 API。
5. 运行 `docker compose ps`、`docker compose logs --tail=50 migrate`；在服务器本地请求 `/api/v1/health/live` 与 `/api/v1/health/ready`，均应得到 200 `{"status":"ok"}`。检查 `schema_migrations` 记录与表数量，并确认现有 80/443 容器仍在。

## 回滚与数据

在部署目录运行 `docker compose down` 可停止本项目，保留 `postgres_data` 卷。不要运行 `down -v`。升级前备份数据库；如果需要回退镜像和源码，恢复上一个 release 目录并重新执行 `docker compose up -d --build`。已应用的数据库 migration 默认不可逆，含迁移的版本回退前需单独审查 SQL 与数据兼容性。

## 公网接入

当前按用户要求开放 18080 用于纯 IP 连通性测试；这个版本只有健康接口，没有可操作的控制台页面。不要在明文 HTTP 上测试未来带 Cookie 的登录功能。域名和证书就绪后，在现有 Nginx Proxy Manager 中创建 HTTPS 代理主机，并将 `CONTROL_BIND_IP` 恢复为 `127.0.0.1`，再关闭公网 18080 访问。
