import { useState, type FormEvent } from "react";
import { Plus, Server } from "lucide-react";
import { mutateCatalog, nodeStatusLabel } from "../../lib/catalog";
import type { ResourceGroup } from "../../lib/admin";
import type { NodeRecord } from "../../lib/catalog";
import { csrfToken } from "../catalog/CreateLine";
import { AdminSection } from "./AdminSection";
import { AdminNodeForm } from "./AdminNodeForm";
import { AdminNodeMetrics } from "./AdminNodeMetrics";
import { AdminAgentEnrollment } from "./AdminAgentEnrollment";
import { revokeAgent } from "./agentEnrollment";

export function ResourcePanel({
  groups,
  nodes,
  canManageNodes,
  canEnrollAgents,
  onRefresh,
}: {
  groups: ResourceGroup[];
  nodes: NodeRecord[];
  canManageNodes: boolean;
  canEnrollAgents: boolean;
  onRefresh: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [region, setRegion] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [metricsNodeID, setMetricsNodeID] = useState("");
  const [busyGroupID, setBusyGroupID] = useState("");
  const [busyNodeID, setBusyNodeID] = useState("");
  const [groupError, setGroupError] = useState("");
  const [nodeError, setNodeError] = useState("");
  const [revokeBusyNodeID, setRevokeBusyNodeID] = useState("");
  async function toggleGroup(group: ResourceGroup) {
    setBusyGroupID(group.id);
    setGroupError("");
    const result = await mutateCatalog<ResourceGroup>(`/api/v1/admin/resource-groups/${group.id}`, "PATCH", { enabled: !group.enabled }, csrfToken());
    setBusyGroupID("");
    if (result.kind === "error") setGroupError(result.message);
    else onRefresh();
  }
  async function toggleNode(node: NodeRecord) {
    setBusyNodeID(node.id);
    setNodeError("");
    const result = await mutateCatalog<NodeRecord>(`/api/v1/admin/nodes/${node.id}`, "PATCH", { enabled: !node.enabled }, csrfToken());
    setBusyNodeID("");
    if (result.kind === "error") setNodeError(result.message);
    else onRefresh();
  }
  async function revokeNodeAgent(node: NodeRecord) {
    if (!window.confirm(`确认撤销节点「${node.name}」当前 Agent？现有连接会被关闭，需要重新入网。`)) return;
    setRevokeBusyNodeID(node.id);
    setNodeError("");
    const result = await revokeAgent(node.id, csrfToken());
    setRevokeBusyNodeID("");
    if (result.kind === "error") setNodeError(result.message);
    else onRefresh();
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    const result = await mutateCatalog<ResourceGroup>(
      "/api/v1/admin/resource-groups",
      "POST",
      { code: code.trim(), name: name.trim(), region: region.trim() },
      csrfToken(),
    );
    setBusy(false);
    if (result.kind === "error") setError(result.message);
    else {
      setOpen(false);
      setCode("");
      setName("");
      setRegion("");
      onRefresh();
    }
  }
  return (
    <AdminSection
      title="资源域与节点"
      description="先建立资源域，再通过节点 Agent 接入服务器。"
      action={canManageNodes ?
        <button
          className="primary-button catalog-create-button"
          type="button"
          onClick={() => setOpen(true)}
        >
          <Server size={16} />
          创建资源域
        </button>
      : undefined}
    >
      {canManageNodes && <div className="admin-inline-actions">
        <AdminNodeForm groups={groups} onSaved={onRefresh} />
      </div>}
      <div className="admin-resource-grid">
        {canManageNodes && <div>
          <h3>资源域</h3>
          {groups.map((group) => (
            <div className="admin-row" key={group.id}>
              <strong>{group.code}</strong>
              <span>
                {group.name} · {group.region}
              </span>
              <em>{group.enabled ? "启用" : "停用"}</em>
              <button className="refresh-button" type="button" disabled={busyGroupID !== ""} onClick={() => void toggleGroup(group)}>{busyGroupID === group.id ? "处理中…" : group.enabled ? "停用资源域" : "启用资源域"}</button>
            </div>
          ))}
          {groups.length === 0 && (
            <div className="catalog-form-note">暂无资源域。</div>
          )}
        </div>}
        <div>
          <h3>节点</h3>
          {nodes.map((node) => (
            <div className="admin-node-entry" key={node.id}>
              <div className="admin-row">
                <strong>{node.name}</strong>
                <span>{node.group_code} · {node.host} · {node.latency_ms == null ? '延迟未知' : `控制面延迟 ${node.latency_ms} ms`}</span>
                <em>{nodeStatusLabel(node.agent_status)}</em>
                <em>{node.enabled ? "已启用" : "已停用"}</em>
                <button className="refresh-button" type="button" aria-expanded={metricsNodeID === node.id} onClick={() => setMetricsNodeID((current) => current === node.id ? "" : node.id)}>{metricsNodeID === node.id ? "收起指标" : "查看指标"}</button>
                {canManageNodes && <button className="refresh-button" type="button" disabled={busyNodeID !== ""} onClick={() => void toggleNode(node)}>{busyNodeID === node.id ? "处理中…" : node.enabled ? "停用节点" : "启用节点"}</button>}
                {canManageNodes && <AdminNodeForm groups={groups} node={node} onSaved={onRefresh} />}
                {canEnrollAgents && <AdminAgentEnrollment nodeID={node.id} nodeName={node.name} />}
                {canEnrollAgents && node.agent_status !== "unknown" && <button className="refresh-button" type="button" disabled={revokeBusyNodeID !== ""} onClick={() => void revokeNodeAgent(node)}>{revokeBusyNodeID === node.id ? "撤销中…" : "撤销 Agent"}</button>}
              </div>
              {metricsNodeID === node.id && <AdminNodeMetrics nodeID={node.id} />}
            </div>
          ))}
          {nodes.length === 0 && (
            <div className="catalog-form-note">
              暂无节点，请先创建节点记录，再签发 Agent 入网令牌。
            </div>
          )}
        </div>
      </div>
      {groupError && <p className="catalog-page-error" role="alert">{groupError}</p>}
      {nodeError && <p className="catalog-page-error" role="alert">{nodeError}</p>}
      {canManageNodes && open && (
        <div className="catalog-dialog-backdrop" role="presentation">
          <form
            className="catalog-dialog"
            onSubmit={(event) => void submit(event)}
          >
            <div className="catalog-dialog-head">
              <span className="section-overline">NEW RESOURCE GROUP</span>
              <h2>创建资源域</h2>
              <p>资源域用于套餐授权和节点分组。</p>
            </div>
            <label htmlFor="admin-group-code">资源域编码</label>
            <input
              id="admin-group-code"
              required
              placeholder="RFC.JPT1"
              value={code}
              onChange={(event) => setCode(event.target.value.toUpperCase())}
            />
            <label htmlFor="admin-group-name">名称</label>
            <input
              id="admin-group-name"
              required
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <label htmlFor="admin-group-region">地区</label>
            <input
              id="admin-group-region"
              required
              value={region}
              onChange={(event) => setRegion(event.target.value)}
            />
            {error && (
              <div className="auth-error" role="alert">
                {error}
              </div>
            )}
            <div className="catalog-form-actions">
              <button
                className="refresh-button"
                type="button"
                onClick={() => setOpen(false)}
              >
                取消
              </button>
              <button className="primary-button" disabled={busy}>
                {busy ? "创建中…" : "创建资源域"}
                <Plus size={16} />
              </button>
            </div>
          </form>
        </div>
      )}
    </AdminSection>
  );
}
