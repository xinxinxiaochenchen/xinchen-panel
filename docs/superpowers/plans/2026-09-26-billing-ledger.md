# 账期、用量账本与额度执行 Implementation Plan

> **For agentic workers:** Use executing-plans to implement task by task. This continues the approved overall design; no payment, recharge or orders.

**Goal:** 将上传/下载用量归入冻结账期，按倍率幂等扣除额度，并最终在 Agent 数据面执行。

**Architecture:** `internal/billing` 负责日历账期、整数计费、PostgreSQL 账本及租约事务。账期、连接快照、追加事件、租约各自建模；只有入口计量。Agent 上报累积计数，控制面存储增量事件，按连接累计计费量差值扣量。当前部署保持只读，全部额度执行接通前不开放真实代理。

**Tech Stack:** Go, PostgreSQL 16, existing pgx/migrations/Agent mTLS WebSocket.

## 约定

- 时间区间为 `[starts_at, ends_at)`，重置发生在套餐时区锚定日当地日界。短月取月末，下个月恢复原锚定日。初期从订购生效时间开始，末期截断在订购到期时间。自定义锚定日晚于生效日时，首期截至同月该日；否则截至之后第 `period_months` 月的锚定日。DST 跳过日界时使用当日第一个实际时刻。
- 首期使用订购冻结快照；续期在创建事务中冻结最新套餐与授权。账期时间表固定，不能靠对已截断的上次结束时间 AddDate 推算。
- 倍率以千分比表示，计费取 `floor((累计上传+累计下载)*multiplier/1000)`；每次事件保存与前一次累计计费的差值，避免报告拆分造成误差。检查 bigint 溢出。连接冻结倍率和账期，不接受 Agent 自报用户、倍率或授权。
- 连接序号从 1 开始、严格连续；重复同序号同内容为成功，内容不同为冲突。旧账期迟到报告归回旧账期，不能扣新账期。以连接 UUID + 序号去重。
- 发放租约锁定账期行，确保已计费+未结租约预留不超过额度。租约请求使用幂等请求 ID；同一请求重试不能再次发放。过期不直接释放未确认字节，避免离线用量丢失导致超发；对账或保守结算后再释放。

## Task 1: 日历与整数计费基础

Files: `internal/billing/calendar.go`, `calendar_test.go`, `meter.go`, `meter_test.go`.

- [x] 测试先行：31/30/29 锚定日、闰年、短月后恢复、首末期、季度、时区/DST、无效配置和范围。
- [x] 运行 billing 包，确认缺失 API 导致失败；实现 `Schedule.PeriodAt(time.Time) (Window,error)`，返回 UTC 半开区间。
- [x] 测试先行：上传+下载、500/1000/2000 倍率、拆分报告、下降计数、非法倍率和 bigint 溢出。
- [x] 实现 `ChargeDelta(previous,current Counters,multiplier int64) (Delta,error)`；中间乘法不能溢出。
- [x] billing 测试、race、vet 通过，提交基础。

## Task 2: 持久化账期与幂等账本

Files: `migrations/000011_billing_ledger.{up,down}.sql`, `internal/billing/{model,repository,period_repository,usage_repository}.go`, `repository_test.go`.

- [x] 独立 PostgreSQL 16 验证迁移 up/down/up。账期唯一且不重叠，连接绑定用户/入口/线路/账期，事件只追加，计数非负。
- [x] 先写集成测试，再实现账期创建的并发幂等与快照、连接绑定及累计报告追加事务。测试重复/冲突/跳号/越权 Agent/倍率冻结/旧账期迟到报告/溢出回滚。
- [x] 同一连接行锁保证上报串行；更新账期聚合与事件插入在同一事务，错误不留下半笔账。

## Task 3: 额度租约与周期推进

Files: `internal/billing/{lease_repository,worker}.go`, matching tests, migration as necessary.

- [ ] 发放/续约/结算事务并发测试：多个 Agent 不超发，重试不重复预留，过期不无条件回收未确认用量。
- [ ] 周期 worker 幂等创建新账期并重新编译授权；用户停用、到期和零额度拒绝新租约。

## Task 4: Agent 用量与执行

Files: `internal/agentproto`, `agentclient`, `agentruntime`, `orchestration`, Agent stream handlers.

- [ ] 报告/ACK/租约协议和限长校验，身份映射到节点，禁止自报倍率。
- [ ] 上传+下载首入口计量、有限租约、超额停止、连接跨账期终止/重建；用量 outbox 持久化及断线重放。
- [ ] 真实 TCP/UDP/Trojan 套接字验证额度耗尽、断线、重启恢复及报告丢失重试。

## Task 5: REST、报表与控制台

Files: billing handlers, `api/openapi/control-plane.yaml`, `apps/web`.

- [ ] 自己的账期/用量/日报 REST 与管理员按用户/入口节点/线路/日期筛选；RBAC、分页和查询跨度限制。
- [ ] 首页展示额度、原始上传下载、计费量、百分比、账期、下次重置和冻结授权。
- [ ] 文档、全量 Go 测试/vet、前端构建、原生浏览器操作与独立数据库验证后发布；正式库升级前备份。

## Verification commands

`GOCACHE=/private/tmp/network-control-plane-gocache GOPATH=/private/tmp/network-control-plane-gopath /private/tmp/network-control-plane-go/go/bin/go test ./internal/billing -count=1`

`... go test -race ./internal/billing -count=1` and `... go vet ./...`.
Database tests require `CONTROL_TEST_DATABASE_URL` targeting a disposable PostgreSQL 16 database only.

## 2026-09-26 基础阶段验证记录

- 已完成 Task 1–2。账期计算、累计千分比倍率计费、迁移 11、冻结连接和只追加事件账本已实现；这些是内部仓储原语，尚未挂接 Agent 或 HTTP。
- 真实 PostgreSQL 16.10 独立库 `codex_billing11_20260926` 验证迁移 11 up/down/up、8 路并发账期创建/连接注册/重复报告、冻结快照与倍率、纳秒时间戳重试、越权 Agent、乱序与下降计数、迟到报告归原账期、计费聚合溢出事务回滚、禁止修改/删除事件。原有套餐生命周期集成测试也通过。测试库已删除。
- 回归先复现并修复并发注册唯一键冲突、同序号改时间未被拒绝、观察时间倒退，以及 PostgreSQL 微秒存储造成纳秒输入原样重试冲突。
- 全量 Go 测试、billing race、vet 均通过。本地未配置数据库时 PostgreSQL 测试会跳过，数据库结论来自上述独立服务器测试。
- 新建订购记录冻结 `period_months`，续期创建冻结最新套餐快照；当前授权快照推进由 Task 3 worker 完成。Task 2 仅负责账本，不能单独提供额度执行保证。
- 正式部署仍为 `a40cd29` / 数据库迁移 10。此阶段未升级正式库或开放登录/Agent/真实代理。
