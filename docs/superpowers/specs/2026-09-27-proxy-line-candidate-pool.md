# 代理连接线路候选池设计

状态：下一阶段实现切片。

## 目标

让一个代理连接可以绑定多个已经授权的线路，入口 Agent 在新 Trojan 连接建立时按线路 `priority`、`weight` 和当前健康状态选择候选；重试只改变新连接的候选，不迁移已经建立的连接。现有单 `line_id` 代理连接继续兼容。

## 数据模型

保留 `proxy_accesses.line_id` 作为兼容字段和默认线路，新增：

```sql
CREATE TABLE proxy_access_lines (
    proxy_access_id uuid NOT NULL REFERENCES proxy_accesses(id) ON DELETE CASCADE,
    line_id uuid NOT NULL REFERENCES lines(id),
    position integer NOT NULL CHECK (position >= 0),
    priority integer NOT NULL DEFAULT 100 CHECK (priority BETWEEN 0 AND 1000),
    weight integer NOT NULL DEFAULT 1 CHECK (weight BETWEEN 1 AND 100),
    PRIMARY KEY (proxy_access_id, line_id),
    UNIQUE (proxy_access_id, position)
);
CREATE INDEX proxy_access_lines_line_idx ON proxy_access_lines(line_id, proxy_access_id);
```

迁移时为每个旧连接插入一行 `position=0`，并保持 `proxy_accesses.line_id` 与位置 0 相同。之后禁止删除位置 0；更新连接时在同一事务内重写映射并再次执行线路逐跳授权。

## API

`POST /api/v1/proxy-accesses` 接受 `line_ids`（至少一个 UUID），保留单值 `line_id` 作为旧客户端输入别名；同时提供 `line_options` 写入优先级和权重。返回值包含兼容的 `line_id` 和完整的 `line_ids` / `line_options`。

服务端规则：

1. 所有线路必须属于同一用户套餐允许的资源域和共享线路集合。
2. 每条线路必须通过完整 hop 校验；多跳线路必须有当前世代、证书和逐跳 ACK。
3. 同一代理连接的线路不能重复，最多受套餐 `max_proxy_lines` 限制；旧套餐缺省值为 1。
4. 移除线路前先确认没有订阅导出或活跃连接需要它；已有计费会话保留冻结线路。

## Agent 快照

`agentruntime.ProxyAccess` 保留 `LineID`，新增：

```go
type ProxyLineCandidate struct {
    LineID          string `json:"line_id"`
    RelayGeneration uint64 `json:"relay_generation,omitempty"`
    Priority        int    `json:"priority"`
    Weight          int    `json:"weight"`
}
```

`Candidates` 为空时按旧字段合成一个候选；非空时允许暂时不包含 `LineID` 对应的默认线路，以便默认线路停用、失去授权或未完成中继 ACK 时由已授权的候选接管。`LineID` 仍是数据库兼容字段，不代表 Agent 一定能使用该线路。快照校验拒绝重复线路、非法权重、缺少候选世代或与本 Agent 入口不匹配的候选。只剩非默认线路时，Agent 必须使用能记录实际 `line_id` 的计量接口。

控制面只把当前入口节点可接受的线路放进该 Agent 的快照。其它入口节点收到同一代理凭据时拥有自己的候选子集，因此每次连接都能在实际可达的入口上稳定选择。

## 选择、计费和重试

1. Trojan 认证只确认代理凭据，不立即开计量会话。
2. Agent 过滤本地已应用且未过期的候选；多跳候选必须存在同世代 ingress route。
3. 使用连接 ID 与线路 ID 的 SHA-256 计算确定性加权顺序；先取最小 `priority` 层，再按 `weight` 排列候选。
4. 对每个候选最多尝试一次。只有真正拨号成功的线路才调用 `Meter.Open`，因此一次逻辑连接只产生一个 `usage_session`。
5. `QuotaOpenRequest` 和 Agent 协议增加可选 `line_id`。控制面在 `proxy_access_lines` 中锁定并授权该线路，把最终线路写入 `usage_sessions.line_id`；重复请求必须复用同一冻结线路。
6. 续租根据 `usage_sessions.line_id` 重新授权，不允许代理连接在会话中切换到另一个候选。

## 兼容和回滚

- 旧 Agent 收到非空 `candidates` 时必须 NACK，不得静默回退；控制面检测能力后只向旧 Agent 下发单候选兼容快照。
- 迁移 down 先删除候选映射，再恢复 `proxy_accesses.line_id` 的原有约束；历史计费表不回写。
- 同一代理凭据和入口下的候选是服务端选择，客户端不能直接指定某条候选线路。订阅导出应保留每个代理连接一个客户端目标，并明确它是自动线路；绑定具体线路的客户端分流策略只能引用可独立选择的目标。现有导出仍只处理默认线路，在修正导出语义前不得开放多线路创建。

## 验收

- 两条同优先级线路按固定连接 ID 得到稳定且可重试的顺序。
- 健康线路撤销后不再出现在 Agent 快照；单候选旧连接行为保持不变。
- 第一个候选拨号失败时第二个候选成功，数据库只有一个入口计量会话且 `line_id` 是最终线路。
- 同一 `request_id` 重试不会重复预留额度；续租和结算始终使用冻结线路。
