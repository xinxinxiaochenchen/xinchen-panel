import { useEffect, useMemo, useRef, useState } from "react";
import { BarChart3, RefreshCw } from "lucide-react";
import type { User } from "../../lib/dashboard";
import { dailyRange, formatBytes } from "../../lib/dashboard";
import type { LineRecord, NodeRecord } from "../../lib/catalog";
import { adminUsageDimensionLabel, loadAdminUsagePage, validateAdminUsageFilters, type AdminUsageFilters, type AdminUsageGroup, type AdminUsageNames, type UsageBucket } from "../../lib/admin";
import { AdminSection } from "./AdminSection";

type Props = { users: User[]; nodes: NodeRecord[]; lines: LineRecord[] };

const groupLabels: Record<AdminUsageGroup, string> = { date: "日期", user: "用户", node: "入口节点", line: "线路" };

function UsageTable({ items, filters, names }: { items: UsageBucket[]; filters: AdminUsageFilters; names: AdminUsageNames }) {
  return <div className="admin-usage-table" role="table" aria-label="管理员流量统计">
    <div className="admin-usage-row admin-usage-heading" role="row">
      <span>日期</span><span>{groupLabels[filters.groupBy]}</span><span>上传</span><span>下载</span><span>计费流量</span>
    </div>
    {items.map((item, index) => <div className="admin-usage-row" role="row" key={`${item.date}:${item.dimension_id}:${index}`}>
      <strong>{item.date}</strong>
      <span>{adminUsageDimensionLabel(filters.groupBy, item.dimension_id, names)}</span>
      <span>{formatBytes(item.uploaded_bytes)}</span>
      <span>{formatBytes(item.downloaded_bytes)}</span>
      <strong>{formatBytes(item.charged_bytes)}</strong>
    </div>)}
  </div>;
}

export function AdminUsage({ users, nodes, lines }: Props) {
  const initialRange = dailyRange(new Date(), 14);
  const [from, setFrom] = useState(initialRange.from);
  const [to, setTo] = useState(initialRange.to);
  const [groupBy, setGroupBy] = useState<AdminUsageGroup>("date");
  const [userID, setUserID] = useState("");
  const [nodeID, setNodeID] = useState("");
  const [lineID, setLineID] = useState("");
  const [items, setItems] = useState<UsageBucket[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [generation, setGeneration] = useState(0);

  const filters = useMemo(() => ({ from, to, groupBy, userID: userID || undefined, nodeID: nodeID || undefined, lineID: lineID || undefined }), [from, to, groupBy, userID, nodeID, lineID]);
  const requestKey = JSON.stringify([filters, generation]);
  const currentKey = useRef(requestKey);
  currentKey.current = requestKey;
  const names = useMemo<AdminUsageNames>(() => ({
    users: new Map(users.map((user) => [user.id, user.email])),
    nodes: new Map(nodes.map((node) => [node.id, `${node.name} · ${node.group_code}`])),
    lines: new Map(lines.map((line) => [line.id, line.name])),
  }), [users, nodes, lines]);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    setItems([]);
    setCursor(null);
    const validation = validateAdminUsageFilters(filters);
    if (validation) {
      setError(validation);
      setLoading(false);
      return () => { active = false; };
    }
    void loadAdminUsagePage(filters).then((result) => {
      if (!active || currentKey.current !== requestKey) return;
      setLoading(false);
      if (result.kind === "error") { setError(result.message); return; }
      if (result.kind === "empty") return;
      setItems(result.data.items);
      setCursor(result.data.next_cursor);
    });
    return () => { active = false; };
  }, [filters, generation, requestKey]);

  async function loadMore() {
    if (!cursor || loading) return;
    const key = requestKey;
    setLoading(true);
    const result = await loadAdminUsagePage(filters, cursor);
    if (currentKey.current !== key) return;
    setLoading(false);
    if (result.kind === "error") { setError(result.message); return; }
    if (result.kind === "ready") { setItems((previous) => [...previous, ...result.data.items]); setCursor(result.data.next_cursor); }
    else setCursor(null);
  }

  return <AdminSection title="流量统计" description="按日期、用户、入口节点或线路查看上传、下载和计费流量。统计数据使用 UTC 日期。" action={<button className="refresh-button" type="button" onClick={() => setGeneration((value) => value + 1)}><RefreshCw size={16} />刷新</button>}>
    <div className="admin-usage-filters">
      <label>开始日期<input type="date" value={from} onChange={(event) => setFrom(event.target.value)} /></label>
      <label>结束日期<input type="date" value={to} onChange={(event) => setTo(event.target.value)} /></label>
      <label>分组<select value={groupBy} onChange={(event) => setGroupBy(event.target.value as AdminUsageGroup)}>{Object.entries(groupLabels).map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select></label>
      <label>用户<select value={userID} onChange={(event) => setUserID(event.target.value)}><option value="">全部用户</option>{users.map((user) => <option value={user.id} key={user.id}>{user.email}</option>)}</select></label>
      <label>入口节点<select value={nodeID} onChange={(event) => setNodeID(event.target.value)}><option value="">全部节点</option>{nodes.map((node) => <option value={node.id} key={node.id}>{node.name}</option>)}</select></label>
      <label>线路<select value={lineID} onChange={(event) => setLineID(event.target.value)}><option value="">全部线路</option>{lines.map((line) => <option value={line.id} key={line.id}>{line.name}</option>)}</select></label>
    </div>
    {loading && items.length === 0 && <div className="catalog-state" role="status">正在加载流量统计…</div>}
    {error && <div className="catalog-state catalog-error" role="alert">{error}</div>}
    {!loading && !error && items.length === 0 && <div className="empty-state"><BarChart3 size={18} />当前区间暂无流量记录</div>}
    {items.length > 0 && <UsageTable items={items} filters={filters} names={names} />}
    {cursor && <button className="refresh-button" type="button" onClick={() => void loadMore()} disabled={loading}>{loading ? "加载中…" : "加载更多"}</button>}
  </AdminSection>;
}
