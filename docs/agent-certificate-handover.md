# Agent 控制流证书交接

Agent 在证书到期前六小时调用 mTLS `POST /api/v1/agent/renew`，把获得的证书原子写入 `agent.crt`。保存成功后，Agent 在现有 WebSocket `/api/v1/agent/stream` 上发送协议版本 1 消息：

```json
{"type":"certificate_update","payload":{"certificate_pem":"-----BEGIN CERTIFICATE-----..."}}
```

实际帧还包含标准 `protocol_version`、`message_id`、`node_id` 和 `sent_at`。控制面核对新证书绑定的节点、签名 CA、客户端用途、与原 mTLS 证书相同的 Ed25519 公钥，以及数据库中的当前授权。通过后，在这条连接上将后续 15 秒身份复核切换到新证书，并返回：

```json
{"type":"certificate_update_ack","payload":{"fingerprint":"64 个小写十六进制 SHA-256 字符"}}
```

Agent 只接受与待确认的新证书指纹一致的确认；未确认时每 10 秒重发。服务器对同一证书重复确认。控制流异常断线后，Agent 依照现有失联规则关闭数据运行时，新连接从磁盘加载新证书；配置租约到期和节点吊销也保持关闭行为。

上线顺序先升级控制面，再升级 Agent。旧版 Agent 不发送新消息；新版 Agent 向不支持该消息的旧控制面发送时可能被断开。当前公网预览未启动 Agent，真实节点入网后仍需验证长期运行、转发会话连续性和吊销。
