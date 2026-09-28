# 正式版开发与 HTTP 部署

## 目标

完成可以部署使用的正式版开发，现场部署和真实节点验收另做。管理面支持纯 IP HTTP，HTTPS 由使用者选择。现有节点、线路、转发、Trojan 代理、订阅、分流、套餐、用量、RBAC 和审计功能均须在正式部署中可访问。

## 设计

沿用 Go/React/PostgreSQL 架构和业务实现。安装器提供 `--public-http`（可操作 IP HTTP）、`--https`（回环监听供 HTTPS 反代）和 `--public-preview`（只读）。首次无参数安装默认正式 HTTP；无参数更新保留原模式，模式切换显式指定。

HTTP 管理只改变浏览器访问和 Cookie 策略。Agent mTLS 和 Trojan TLS 按原协议使用，可以使用带 IP SAN 的证书，不要求管理域名。

- `CONTROL_BROWSER_COOKIE_SECURE` 在服务配置默认 true；正式 HTTP 安装设 false，HTTPS 设 true。
- HTTPS 使用现有 `__Host-control_session`/`__Host-control_csrf`，HTTP 使用不带 `__Host-` 的 host-only Cookie。均保持 HttpOnly 会话、SameSite=Lax、CSRF 和 RBAC。退出、改密、所有鉴权使用相同策略。
- 前端请求 `/api/v1/me`：401 显示登录，404 才显示预览，登录后加载实际功能；CSRF、复制及文案适用于 HTTP。
- 正式安装生成并持久保存数据库密码及代理凭据密钥，更新保留密钥和数据卷；迁移前备份，失败停止。
- 首次正式部署初始化管理员，密码静默输入或从私有文件读取，经标准输入传递。重试依据数据库判断已有管理员，不重置密码。
- 更新保留已配置的 Agent/relay overlay，文档提供完整的 HTTP、HTTPS、Agent 配置步骤。

## 开发验证

测试覆盖 HTTP Cookie 往返、CSRF、退出/改密、HTTPS 兼容、前端身份判定及复制回退、首次初始化和可重复升级。运行 Go、WebUI、安装器回归、类型检查、生产构建及 Linux 编译。服务器现场验收不属于本轮。
