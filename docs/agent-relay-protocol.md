# Agent 中继握手协议（开发中）

`internal/agentrelay` 提供独立编解码与授权校验，目前尚未接入 Agent 监听器。控制面的 WebSocket 协议版本不变；以下是节点之间的数据通道握手。

## 帧与顺序

每个握手帧由 4 字节无符号大端长度和 UTF-8 JSON 对象组成。JSON 长度必须为 1–4096 字节。字段名区分大小写；拒绝未知字段、重复字段、额外 JSON、非对象输入。读取器只读指定长度，不提前读取后续数据。

调用方先完成受信任传输的对端认证，再发送 `open`，收到匹配 `connection_id` 的 `open_ok` 才转发用户数据。`open_err` 后必须关闭连接。每条传输连接只接受一次握手，不能复用为多个用户连接。调用方负责握手超时、并发上限和连接撤销。

## OPEN

| 字段 | 要求 |
| --- | --- |
| `version` | 整数 1 |
| `type` | `open` |
| `line_id` | 小写规范 UUID |
| `connection_id` | 小写规范 UUID，同一用户逻辑连接逐跳保持一致 |
| `generation` | 正整数，须匹配本地已应用线路世代 |
| `target_host` | 公网 IP 或合法 DNS 名，最长 253 字节 |
| `target_port` | 1–65535 |
| `sent_at` | RFC 3339 时间戳 |
| `proof` | 64 个小写十六进制字符的 HMAC-SHA256 |

证明密钥为每个线路世代、每条相邻边独立生成的 32 字节随机值。HMAC 消息是 `network-control-plane/relay/open/v1`、一个零字节以及按 Go `Open` 结构体字段顺序编码的 JSON；计算前令 `proof` 为空字符串。发送者应使用 `SignOpen`，接收者使用 `VerifyOpen`，不可自制不同规范化编码。目标、连接、世代和时间均参与证明。

接收时间 `now` 必须满足 `sent_at <= now + 30s` 且 `now < sent_at + 30s`。认证成功后原子登记重放键；同一线路、世代、连接 ID 的重复请求拒绝。窗口有固定容量，满时拒绝新请求，不淘汰仍有效的记录。记录保留 60 秒，覆盖完整时间容差。无效证明不消耗容量。窗口按已应用线路世代持有，不能每次请求重新创建。

`VerifyOpen` 不替代节点身份校验。接收运行时必须把传输对端节点身份与本地线路上一跳绑定，并使用该边的密钥。不得接受请求指定下一跳。出口仍需在连接时解析 DNS、验证实际 IP 为公网并拨号该已验证 IP，防止 DNS 重绑定。密钥、证明和目标地址不得写入日志或审计。

## OPEN_OK / OPEN_ERR

两种响应都包含 `version:1`、`type`、`connection_id`。成功响应无 `error_code`。错误响应仅允许以下代码，不返回内部错误或目标内容：

- `UNAUTHORIZED`
- `STALE_GENERATION`
- `TARGET_REJECTED`
- `UPSTREAM_UNAVAILABLE`
- `CAPACITY_EXCEEDED`

## 节点端口预留

迁移 20 增加可选 `nodes.relay_port`，仅允许具备 `forward` 能力的节点设置，范围为 1024–65535，必须与代理端口不同。预留保留在停用节点上，同时占用 TCP/UDP。数据库唯一约束防止和用户转发抢占；同一登记主机的代理/中继 TCP 端点也不能重叠。端口变更冲突时整个事务回滚，原端口仍保留。

中继公告地址沿用节点的 `host`；实际绑定地址由 Agent 本地配置决定。设置端口只预留资源，不会启动监听，也不会令多跳草稿可执行。

## TLS 身份（基础实现）

`IssueRelayServerCertificate` 使用专用 Agent CA 对节点本机生成的 Ed25519 CSR 签发服务端证书。CSR 自报身份和 SAN 不生效；签发身份为 `spiffe://network-control-plane/relay/{node_id}`，服务端 SAN 为控制面登记的节点 `host`，证书仅有 ServerAuth 用途。现有 Agent 客户端证书保持 `spiffe://network-control-plane/agent/{node_id}` 和 ClientAuth 用途，两种证书不能互换。

`ClientTLSConfig` 强制 TLS 1.3、CA 链、DNS/IP SAN、预期节点 URI 和当前允许的证书指纹。`ServerTLSConfig` 强制 TLS 1.3 和客户端证书，由调用方对数据库中的节点状态及指纹执行在线授权；OPEN 之后仍须检查此来源节点正是该线路的上一跳。配置工具已经实现并经真实 TLS 握手测试，但服务端证书签发 HTTP API、Agent 安装/续签、监听和线路快照尚未接入。

## 独立 TCP 中继执行器

`agentrelay.Handler` 已实现出口与中转连接处理。每个已应用线路世代保留同一 `ReplayWindow`；处理器在 TLS 握手后核对来源节点 URI、线路上一跳、世代、证明和有效期。中转只能使用快照中的 `NextHop` 地址、TLS 配置和下一边密钥重新签名 OPEN；用户输入不能选择下一跳。出口重新解析目标 DNS，拒绝非公网 IP，并仅拨号通过检查的 IP。下游返回成功后才对上游发送 `open_ok`。撤销线路 context 会关闭整条链，遇到对端不读 TLS 关闭通知时直接关闭底层连接，防止连接清理停顿。

独立测试使用真实回环 TCP 和三段 TLS 会话验证入口 → 中转 → 出口的数据往返及撤销。此执行器尚未由 Agent 配置快照调用，生产中继端口也没有监听。用户计费只允许在未来的入口代理运行时执行，不能在中转或出口重复记录。
