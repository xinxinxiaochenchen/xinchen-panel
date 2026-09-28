# 网页初始化实施计划

**Goal:** 首次部署无需命令行管理员参数，管理员和业务配置在面板完成。

**Architecture:** 私有自动生成凭证保护网页初始化，PostgreSQL 完成标记和事务锁保证一次性创建；复用现有业务管理表单。

**Tech Stack:** Go / PostgreSQL / React / Docker Compose / Shell / Python unittest。

按 executing-plans 在当前分支顺序执行，沿用用户已授权的开发和 GitHub 更新。

## 1. 后端

- [x] 新增 HTTP 初始化状态与创建测试，先观察 404 失败。
- [x] 新增私有凭证配置测试，先观察缺少配置加载失败。
- [x] 在 `internal/identity/browser_setup.go` 提供初始化状态与创建服务；在 `bootstrap.go` 复用加锁事务、持久化完成标记和审计。
- [x] 新增 migration 27，为已有管理员记录永久完成状态。
- [x] 在 `httpapi/setup.go` 注册 GET/POST `/api/v1/setup`，配置无凭证关闭、错误凭证拒绝、完成冲突、同源保护和限流。
- [x] 将服务接入 `cmd/control-plane/main.go`，执行目标 Go 测试。

## 2. 面板

- [x] 先写 `loadViewer` 未初始化回归测试并观察失败。
- [x] 增加 `InitialSetup.tsx`，提交凭证、邮箱及确认密码，成功后刷新登录状态。
- [x] `loadViewer` 在 401 后读取初始化状态；旧服务 404 保留登录行为。
- [x] 管理页补充业务配置引导，执行前端测试和构建。

## 3. 部署与交付

- [x] 修改部署测试，验证无需管理员环境变量、不调用创建命令、凭证权限和重试保留，观察旧脚本失败。
- [x] 自动生成并挂载 `setup.token`；删除邮箱/密码提示，健康后提示在面板完成初始化。
- [x] 更新 README、部署说明、OpenAPI 与发布包内容。
- [x] 运行全量 Go、vet、前端、Python、Shell 与 diff 验证；记录数据库集成测试是否实际执行。
- [x] 完成自查并提交到 GitHub main（功能提交 `5e079ae`）；公网 18080 登录页已复核，VPS 本轮未升级。

## 验证记录

全量 `go test ./... -count=1`、`go vet ./...`、前端 56 项测试和生产构建、Python 37 项部署/安装/打包测试、Shell 语法、OpenAPI 引用、Compose YAML 和 diff 检查通过。两项 PostgreSQL 初始化集成测试因本机未配置测试 DSN 跳过；未进行服务器现场验收。额外回归复现并修复错误凭证限流、长邮箱拒绝及非 root Docker 用户读取初始化凭证的问题。
