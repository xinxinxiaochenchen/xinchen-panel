# Agent 用量报告与重放

本页描述已经实现的报告通道、连接准入和额度租约。Agent 已接入额度请求、本地计量与崩溃恢复。正式环境仍需完成节点入网、受信任的 HTTPS 管理入口与真实通流验收。

## 消息

使用现有 mTLS WebSocket `/api/v1/agent/stream` 和协议版本 1。Envelope 中的 `node_id` 必须与客户端证书身份一致。重发使用新的 envelope message ID 和发送时间，保留原批次 ID、报告序号、计数与观察时间。

控制面每 15 秒通过 WebSocket Ping/Pong 测量控制面到 Agent 的往返延迟，并写入节点状态。该延迟不计入用户流量，也不依赖 Agent 心跳中的 CPU、内存或网卡计数；Agent 离线、连接撤销或采样超过 45 秒时，目录和指标 API 返回 `latency_ms: null`。

`usage_batch` 由 Agent 发送：

```json
{
  "batch_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  "reports": [{
    "connection_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
    "lease_id": "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
    "sequence": 1,
    "uploaded_bytes": 1024,
    "downloaded_bytes": 2048,
    "observed_at": "2026-09-26T01:02:03.123456Z"
  }]
}
```

- 一个批次 1–128 个报告，payload 最多 256 KiB，整个 envelope 仍限制 1 MiB。
- 每个报告为同一逻辑连接的累计有效载荷字节；序号从 1 连续递增。上传、下载和总计不能超出有符号 bigint。
- 时间为微秒精度；UUID 在解码后转为小写，时间转 UTC。拒绝未知字段、字段大小写别名、重复字段和同连接同序号的重复项。
- 报告不接收用户、账期和倍率字段。控制面按认证节点查 `agents.id`，再由账本核对连接归属。连接须先由控制面建立并冻结其归属。
- 账期允许新连接的区间为 `[start,end)`；截止时刻的最终累计快照允许 `observed_at == end`，超过截止时间的报告拒绝。执行器仍必须在截止前停止转发。

全部报告成功持久化后，控制面发送 `usage_ack`：

```json
{
  "batch_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  "sha256": "<64 个小写十六进制字符>"
}
```

摘要是规范化 `UsageBatch` 的 Go JSON 编码 SHA-256，保留数组顺序。每个连接报告独立事务入账；批次中途失败不会发送 ACK。重连重发完整批次时，已入账部分通过 `(connection_id,sequence)` 去重，内容改变则冲突。ACK 仅表示报告已落库，不表示租约结算或额度续发。

## Agent 持久化

- 文件为 `CONTROL_AGENT_STATE_FILE + ".usage.json"`，模式 `0600`。同一路径由独立 `.lock` 文件排他锁定，进程退出或 `Close` 后释放；支持 Linux/macOS。
- 最多保存 128 个待确认批次，满队列必须阻止上层继续产生未记录用量。写入临时文件、fsync、rename、同步目录后才能返回成功。存储故障后禁止继续写，须关闭并恢复文件。
- 读取严格校验根 JSON、文件类型、权限、大小和每个批次；拒绝符号链接及损坏状态。
- `Pending` 返回深拷贝；同批次 ID 同内容重复入队幂等，不同内容冲突。只有相同 ID 和摘要的 ACK 能删除报告。
- 单连接只发送一个未 ACK 批次，每秒检查新增报告。10 秒未确认则断开并按现有退避重连；旧报告和序号不变。
- 重连先对账未确认报告，再首次应用配置。已运行时，用量等待不会阻挡配置撤销。配置快照、续租与 ACK 由单一接收状态机处理。
- 配置租约使用独立到期观察器关闭执行器；发送或磁盘保存阻塞不能延长有效期。

## 连接准入与额度消息（控制面已接入）

同一条 mTLS WebSocket 流承载以下消息。Agent 发送 `connection_open`，字段为 `request_id`、`connection_id`、`resource_kind`（`forward`/`proxy`）、`resource_id`、`revision`、`requested_bytes`。后续发送 `quota_request`，字段为 `request_id`、`connection_id`、`revision`、`requested_bytes`。单次请求最多 1 MiB。请求中不允许包含用户、账期或倍率。

控制面在一个事务内验证已应用配置、资源、账户及冻结套餐授权，建立连接会话并发放首份租约。多跳代理连接只在入口申请租约和记录一条用量会话；授权还要求每跳资源组在套餐范围内、节点与 Agent 在线、证书有效、期望配置已应用，且中继配置与线路当前世代一致。套餐的 `max_hops` 限制整条线路。每份租约永久绑定一个连接，未使用的续租也能按原请求 ID 幂等重试。`quota_grant` 返回连接和请求 ID、账期 ID、倍率、租约 ID、发放与已消费字节、租约状态、签发/到期时间及账期结束时间。租约最长 30 秒；重试返回原租约，不延长到期时间。

停止转发且用量批次收到 ACK 后，Agent 发送 `quota_settle`，包含请求 ID、连接 ID、租约 ID 和最终计费消耗字节；控制面返回 `quota_settled`。拒绝时返回 `quota_denied`，错误码限定为 `QUOTA_EXHAUSTED`、`NOT_AUTHORIZED`、`CONFLICT` 或 `RETRY_LATER`。服务端异常不会把数据库错误内容发送给 Agent。

连接的用户、线路、账期和倍率被冻结。资源转移、停用或代理线路变更后，旧连接不能借新配置续租；普通配置版本推进但资源和倍率不变时允许续租。历史报告仅校验冻结归属及连接对应租约，允许原资源删除后的迟到报告。

## 正式验收边界

Agent 客户端已在单读者状态机内按请求 ID 分发额度回复，并在连接断开时取消等待中的请求。生产 Agent 执行器已接入持久租约、TCP/Trojan/UDP 本地计量、用量 outbox、报告 ACK 和重启恢复；配置撤销仍可在用量报告等待期间及时关闭数据面。

本地回环与独立 PostgreSQL 测试覆盖双向有效载荷、倍率舍入、租约截止、额度耗尽、断线、崩溃恢复与跨账期关闭。正式 VPS 仍需完成节点入网、受信任 HTTPS 管理入口，以及真实公网节点的 TCP、UDP、Trojan 和多跳通流验收；这些属于部署环境验收。
