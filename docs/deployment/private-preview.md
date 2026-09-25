# 私有预览部署（`us dmit`）

本部署只验证控制面基础、PostgreSQL 迁移和健康接口。业务 API、前端和 Agent 尚未完成，不对公网发布为正式控制台。

## 布局与边界

- 服务器：Termark 资产 `us dmit`，Debian 12 x86_64，Docker Compose v5。
- 部署目录：`/opt/network-control-plane`。Compose 项目名 `network-control-plane`，不会管理已有的 1Panel 和 Nginx Proxy Manager 容器。
- PostgreSQL 只在 Compose 私有网络中提供服务，不映射主机端口。
- API 映射到主机 `127.0.0.1:18080`。80/443 仍由现有 Nginx Proxy Manager 使用。
- 服务器内存约 1 GiB；数据库和 API 分别设 256 MiB、128 MiB 容器限制。部署后观察内存和重启次数。

## 部署

1. 在开发机用 `scripts/build-linux-amd64.sh` 生成 `bin/control-plane` 与 `bin/migrate`。将这些静态 Linux 二进制、迁移和 Compose 文件打包，通过 Termark 上传到 `/opt/network-control-plane/release.tar.gz` 并解压。这样内存较小的服务器只需拉取 PostgreSQL 镜像，无需编译 Go。上传前确认没有覆盖别的应用，不要包含 `.git` 或 `.env`。
2. 在 `deployments/compose/.env` 写入随机的 URL 安全数据库密码，文件权限设为 `0600`；不要把密码写进 Git 或终端输出。
3. 在 `deployments/compose` 运行 `docker compose config --quiet`，确认配置可解析且只映射 `127.0.0.1:18080`。
4. 运行 `docker compose up -d --build`。预编译镜像从 `scratch` 构建；Compose 等 PostgreSQL 健康后执行一次 `migrate up`，成功后启动 API。
5. 运行 `docker compose ps`、`docker compose logs --tail=50 migrate`；在服务器本地请求 `/api/v1/health/live` 与 `/api/v1/health/ready`，均应得到 200 `{"status":"ok"}`。检查 `schema_migrations` 记录与表数量，并确认现有 80/443 容器仍在。

## 回滚与数据

在部署目录运行 `docker compose down` 可停止本项目，保留 `postgres_data` 卷。不要运行 `down -v`。升级前备份数据库；如果需要回退镜像和源码，恢复上一个 release 目录并重新执行 `docker compose up -d --build`。已应用的数据库 migration 默认不可逆，含迁移的版本回退前需单独审查 SQL 与数据兼容性。

## 公网接入

待控制台域名、MVP 业务模块、登录和安全设置齐备后，在现有 Nginx Proxy Manager 中创建 HTTPS 代理主机，转发到回环地址或共享私有 Docker 网络。域名与证书就绪前不开放公网端口。
