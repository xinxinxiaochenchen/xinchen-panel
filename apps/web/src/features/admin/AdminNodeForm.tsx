import { useEffect, useState, type FormEvent } from "react";
import { Plus, Server } from "lucide-react";
import { mutateCatalog, type NodeRecord } from "../../lib/catalog";
import { buildNodeInput, type ResourceGroup } from "../../lib/admin";
import { csrfToken } from "../catalog/CreateLine";

export function AdminNodeForm({
  groups,
  node,
  onSaved,
}: {
  groups: ResourceGroup[];
  node?: NodeRecord;
  onSaved: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [groupID, setGroupID] = useState("");
  const [name, setName] = useState("");
  const [region, setRegion] = useState("");
  const [host, setHost] = useState("");
  const [publicIP, setPublicIP] = useState("");
  const [bandwidthBPS, setBandwidthBPS] = useState<number | null>(null);
  const [multiplier, setMultiplier] = useState(1);
  const [tagsText, setTagsText] = useState("");
  const [proxy, setProxy] = useState(true);
  const [forward, setForward] = useState(false);
  const [proxyPort, setProxyPort] = useState(443);
  const [relayPort, setRelayPort] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!node) return;
    setGroupID(node.group_id);
    setName(node.name);
    setRegion(node.region);
    setHost(node.host);
    setPublicIP(node.public_ip ?? "");
    setBandwidthBPS(node.bandwidth_bps);
    setMultiplier(node.multiplier_milli / 1000);
    setTagsText(node.tags.join(", "));
    setProxy(node.capabilities.includes("proxy"));
    setForward(node.capabilities.includes("forward"));
    setProxyPort(node.proxy_port ?? 443);
    setRelayPort(node.relay_port);
  }, [node]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const body = buildNodeInput({
        groupID,
        name,
        region,
        host,
        publicIP,
        bandwidthBPS,
        multiplier,
        tags: tagsText.split(",").map((tag) => tag.trim()).filter(Boolean),
        proxy,
        forward,
        proxyPort,
        relayPort,
      });
      const result = await mutateCatalog<NodeRecord>(
        node ? `/api/v1/admin/nodes/${node.id}/config` : "/api/v1/admin/nodes",
        node ? "PATCH" : "POST",
        body,
        csrfToken(),
      );
      if (result.kind === "error") setError(result.message);
      else {
        setOpen(false);
        setName("");
        setHost("");
        setPublicIP("");
        setBandwidthBPS(null);
        setMultiplier(1);
        setTagsText("");
        onSaved();
      }
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "节点资料无效。");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <button
        className={node ? "refresh-button" : "primary-button catalog-create-button"}
        type="button"
        onClick={() => setOpen(true)}
      >
        <Server size={16} />
        {node ? "编辑节点" : "创建节点"}
      </button>
      {open && (
        <div className="catalog-dialog-backdrop" role="presentation">
          <form
            className="catalog-dialog"
            onSubmit={(event) => void submit(event)}
          >
            <div className="catalog-dialog-head">
              <span className="section-overline">{node ? "EDIT NODE" : "NEW NODE"}</span>
              <h2>{node ? "编辑节点" : "创建节点"}</h2>
              <p>{node ? "修改地址或中继参数后，Agent 需要重新申请中继证书并等待配置收敛。" : "节点记录建立后，再签发 Agent 入网令牌并部署 Agent。"}</p>
            </div>
            <label htmlFor="admin-node-group">资源域</label>
            <select
              id="admin-node-group"
              required
              value={groupID}
              onChange={(event) => setGroupID(event.target.value)}
            >
              <option value="">请选择资源域</option>
              {groups
                .filter((group) => group.enabled || group.id === node?.group_id)
                .map((group) => (
                  <option key={group.id} value={group.id}>
                    {group.code} · {group.name}
                  </option>
                ))}
            </select>
            <label htmlFor="admin-node-name">节点名称</label>
            <input
              id="admin-node-name"
              required
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <label htmlFor="admin-node-region">地区</label>
            <input
              id="admin-node-region"
              required
              value={region}
              onChange={(event) => setRegion(event.target.value)}
            />
            <label htmlFor="admin-node-host">IP 或域名</label>
            <input
              id="admin-node-host"
              required
              value={host}
              onChange={(event) => setHost(event.target.value)}
            />
            <label htmlFor="admin-node-public-ip">对外 IP（可选）</label>
            <input
              id="admin-node-public-ip"
              value={publicIP}
              onChange={(event) => setPublicIP(event.target.value)}
              placeholder="订阅或中继使用的公网 IP"
            />
            <label htmlFor="admin-node-bandwidth">带宽（bit/s，可选）</label>
            <input
              id="admin-node-bandwidth"
              type="number"
              min={0}
              step={1}
              value={bandwidthBPS ?? ""}
              onChange={(event) => setBandwidthBPS(event.target.value === "" ? null : Number(event.target.value))}
              placeholder="例如 1000000000"
            />
            <label htmlFor="admin-node-multiplier">节点计费倍率</label>
            <input
              id="admin-node-multiplier"
              type="number"
              min={0.001}
              max={100}
              step={0.001}
              value={multiplier}
              onChange={(event) => setMultiplier(Number(event.target.value))}
            />
            <label htmlFor="admin-node-tags">标签（逗号分隔，可选）</label>
            <input
              id="admin-node-tags"
              value={tagsText}
              onChange={(event) => setTagsText(event.target.value)}
              placeholder="premium, jp1"
            />
            <span className="catalog-form-label">节点能力</span>
            <div className="admin-check-row">
              <label>
                <input
                  type="checkbox"
                  checked={proxy}
                  onChange={(event) => setProxy(event.target.checked)}
                />
                代理出口
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={forward}
                  onChange={(event) => { setForward(event.target.checked); if (!event.target.checked) setRelayPort(null); }}
                />
                转发入口
              </label>
            </div>
            {proxy && (
              <>
                <label htmlFor="admin-node-port">代理端口</label>
                <input
                  id="admin-node-port"
                  type="number"
                  min={1}
                  max={65535}
                  value={proxyPort}
                  onChange={(event) => setProxyPort(Number(event.target.value))}
                />
              </>
            )}
            {forward && <>
              <label htmlFor="admin-node-relay-port">节点间中继端口（可选）</label>
              <input id="admin-node-relay-port" type="number" min={1024} max={65535}
                value={relayPort ?? ""} onChange={(event) => setRelayPort(event.target.value === "" ? null : Number(event.target.value))}
                placeholder="例如 24443" />
              <small className="catalog-form-note">用于 Agent 节点间中继。多跳线路的每个节点都须设置中继端口。</small>
            </>}
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
                {busy ? (node ? "保存中…" : "创建中…") : node ? "保存节点" : "创建节点"}
                <Plus size={16} />
              </button>
            </div>
          </form>
        </div>
      )}
    </>
  );
}
