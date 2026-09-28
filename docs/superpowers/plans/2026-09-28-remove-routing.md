# 删除分流功能实施计划

> 按当前会话依次执行，复用干净工作区，使用 codex/remove-routing 分支。用户已明确授权开发及 GitHub 更新。

**目标：** 删除全部可用分流功能并保持普通订阅、代理连接及转发可用。

**实现：** 去掉 UI/API 和存储模型的分流依赖，导出器只保留默认代理配置；新增迁移 28 清理已发布结构，旧迁移不变。

**技术：** Go、PostgreSQL、React/TypeScript、OpenAPI 3.1。

## 步骤

- [x] 1. 回归：HTTP 订阅创建/PATCH 携带 routing_profile_id 返回 400；前端 payload 只有名称、模板和代理连接。先运行观察失败。
- [x] 2. 删除 internal/routing、internal/georules 及 HTTP 注册、UI 组件和测试；清理 subscriptions 的模型、SQL、授权和导出依赖。
- [x] 3. 简化四种订阅格式的生成器，保留节点凭据/名称校验、回环监听和默认全局代理。保留有意义的普通导出测试。
- [x] 4. 删除套餐限额、管理页、首页和权限列表中的分流字段。新增 migrations/000028_remove_routing.{up,down}.sql 和数据库升级回归。
- [x] 5. 更新 api/openapi/control-plane.yaml、README.md、docs/deployment/vps-webui.md 和历史文档标记。
- [x] 6. 执行 Go 全量测试/vet、WebUI 测试/构建、部署脚本测试和语法、OpenAPI 引用校验、Linux amd64 构建及 diff 检查。具备 PostgreSQL 时执行迁移回归。
- [x] 7. 独立复审全部删除范围、SQL、迁移、客户端配置、前端依赖及 OpenAPI；未发现可执行问题，验证限制已记录。
- [x] 8. 功能提交 `1c795d6` 已快进推送 GitHub main，远端提交已复核。

## 命令

```sh
GOCACHE=/private/tmp/ncp-gocache GOPATH=/private/tmp/ncp-gopath /private/tmp/ncp-toolchain/go/bin/go test ./... -count=1
GOCACHE=/private/tmp/ncp-gocache GOPATH=/private/tmp/ncp-gopath /private/tmp/ncp-toolchain/go/bin/go vet ./...
npm --prefix apps/web test
npm --prefix apps/web run build
python3 -m unittest discover -s scripts -p 'test_*.py'
sh -n scripts/deploy-vps.sh scripts/install-vps.sh
git diff --check
```

## 已执行验证

- Go 全量测试及 vet、WebUI 52 项测试与生产构建、37 项脚本回归、shell 语法、六个 Linux amd64 命令构建、OpenAPI 引用与六个 Compose YAML 检查通过。
- 订阅创建/PATCH 拒绝旧绑定、套餐创建拒绝旧限额、历史冻结快照保留其它授权的回归通过。
- 历史 1–27 共 54 个迁移文件与基线提交的 SHA-256 相同。
- 临时 PGlite PostgreSQL 18.3 验证新装全部 28 个迁移、有旧分流数据的升级、事务回滚、up/down/up 和改动后的订阅/套餐仓储 SQL。新 SQL 能正常创建、修改和查询业务数据。
- 本机未配置 CONTROL_TEST_DATABASE_URL，真实 PostgreSQL 仓储及升级集成测试跳过；没有执行 VPS 部署和现场验收。原生客户端二进制未配置，相关可选测试跳过。
