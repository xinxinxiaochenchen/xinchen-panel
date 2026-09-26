import { useEffect, useState } from "react";
import { History, RefreshCw } from "lucide-react";
import type { AuditRecord } from "../../lib/admin";
import { loadCatalogPage } from "../../lib/catalog";
import { AdminSection } from "./AdminSection";

function formatTime(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", {
    dateStyle: "medium",
    timeStyle: "medium",
  }).format(new Date(value));
}

export function AdminAudit() {
  const [items, setItems] = useState<AuditRecord[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    void loadCatalogPage<AuditRecord>("/api/v1/admin/audit", null).then((result) => {
      if (!active) return;
      setLoading(false);
      if (result.kind === "error") {
        setError(result.message);
        return;
      }
      if (result.kind === "empty") {
        setItems([]);
        setCursor(null);
        return;
      }
      if (result.kind !== "ready") {
        setError("审计列表状态无效。");
        return;
      }
      setItems(result.data.items);
      setCursor(result.data.next_cursor);
    });
    return () => {
      active = false;
    };
  }, [generation]);

  async function loadMore() {
    if (!cursor || loading) return;
    setLoading(true);
    const result = await loadCatalogPage<AuditRecord>("/api/v1/admin/audit", cursor);
    setLoading(false);
    if (result.kind === "error") {
      setError(result.message);
      return;
    }
    if (result.kind === "ready") {
      setItems((previous) => [...previous, ...result.data.items]);
      setCursor(result.data.next_cursor);
    } else {
      setCursor(null);
    }
  }

  return (
    <AdminSection
      title="审计日志"
      description="记录资源、账户、权限和配置变更。敏感凭据不会在列表中展示。"
      action={
        <button className="refresh-button" type="button" onClick={() => setGeneration((value) => value + 1)}>
          <RefreshCw size={16} />刷新
        </button>
      }
    >
      {loading && items.length === 0 && <div className="catalog-state" role="status">正在加载审计日志…</div>}
      {error && <div className="catalog-state catalog-error" role="alert">{error}</div>}
      {!loading && !error && items.length === 0 && (
        <div className="empty-state"><History size={18} />暂无审计记录</div>
      )}
      {items.length > 0 && (
        <div className="admin-table">
          {items.map((item) => (
            <div className="admin-row" key={item.id}>
              <strong>{item.action} · {item.object_type}</strong>
              <span>{item.actor_email} · {formatTime(item.created_at)}</span>
              <small>{item.object_id} · 请求 {item.request_id}</small>
            </div>
          ))}
        </div>
      )}
      {cursor && <button className="refresh-button" type="button" onClick={() => void loadMore()} disabled={loading}>{loading ? "加载中…" : "加载更多"}</button>}
    </AdminSection>
  );
}
