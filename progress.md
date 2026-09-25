# 进度

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
