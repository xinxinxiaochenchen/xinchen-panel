# 正式版 HTTP 部署实施计划

**Goal:** 完成纯 IP HTTP 或自选 HTTPS 下可操作、可更新的正式版本；现场验收延后。

**Architecture:** 保留业务仓储和 Agent 协议。配置控制浏览器 Cookie；前端按服务响应判断登录或预览；安装器提供正式配置、密钥和管理员初始化。

**Tech Stack:** Go 1.27.1、React 19、TypeScript、PostgreSQL 16、POSIX sh、Docker Compose。

## 任务 1：HTTP 会话

- [x] 先写 HTTP cookie jar 登录、鉴权、CSRF、退出及 Cookie 配置回归，观察失败。
- [x] 在 `internal/platform/config/config.go`、`internal/platform/httpapi` 和 `cmd/control-plane/main.go` 实现统一 Cookie 策略。
- [x] `go test ./internal/platform/config ./internal/platform/httpapi ./cmd/control-plane` 通过。

## 任务 2：前端 HTTP 功能

- [x] 在 `apps/web/tests/dashboard.test.ts` 覆盖 HTTP guest、signed-in、preview，并添加 CSRF/复制回归，观察失败。
- [x] 修改 `dashboard.ts`、`useViewer.ts`、`App.tsx`、`CreateLine.tsx` 和复制按钮，按服务响应开放完整 UI。
- [x] `npm --prefix apps/web test`、`npm --prefix apps/web run build` 通过。

## 任务 3：正式安装与更新

- [x] 为 `scripts/deploy-vps.sh` 和 `install-vps.sh` 先写三模式、首次初始化、保留配置、失败重试回归。
- [x] 提供 `--public-http`、`--https`、`--public-preview`，无参数更新保留模式。
- [x] 正式 overlay 挂载密钥并设置 Cookie 策略；更新保留已配置 Agent/relay overlay。
- [x] 根据数据库状态实现可重复管理员初始化，密码仅经标准输入传递。
- [x] `python3 -m unittest discover -s scripts -p 'test_*.py'` 和 shell 语法检查通过。

## 任务 4：交付与审查

- [x] README、部署说明、OpenAPI、示例配置和发布包同步；历史服务器记录保留。
- [x] 核对现有功能 UI/API，修复阻止部署使用的开发缺口。
- [x] Go 全量测试/vet、WebUI 回归/构建、脚本回归、Linux amd64 编译、OpenAPI 解析与 diff 检查通过。
- [x] 独立审查要求覆盖与代码质量，修复问题，更新开发记录。现场验收另做。

## 开发结果

2026-09-28 完成全部开发任务。审查发现的密钥权限重试、预览切换后的密钥保存、旧布尔配置兼容和 Cookie 模式切换问题均修复并经回归验证。最终验证与发布包记录见 `progress.md`；本轮未连接 VPS，数据库集成测试因缺少测试 DSN 跳过。
