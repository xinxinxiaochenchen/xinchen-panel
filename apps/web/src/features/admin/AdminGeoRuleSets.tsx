import { useState, type ChangeEvent, type FormEvent } from "react";
import { Database, Upload } from "lucide-react";
import { mutateCatalog } from "../../lib/catalog";
import type { GeoRuleSetRecord } from "../../lib/admin";
import { csrfToken } from "../catalog/CreateLine";
import { parseRuleSetFile, type GeoRuleSetKind } from "./geoRuleSetInput";
import { AdminSection } from "./AdminSection";

export function GeoRuleSetPanel({ sets, canWrite, onRefresh }: { sets: GeoRuleSetRecord[]; canWrite: boolean; onRefresh: () => void }) {
  const [kind, setKind] = useState<GeoRuleSetKind>("geosite");
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [version, setVersion] = useState("");
  const [source, setSource] = useState("");
  const [entries, setEntries] = useState<string[]>([]);
  const [fileName, setFileName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [toggleID, setToggleID] = useState("");

  async function chooseFile(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (!file) return;
    try {
      if (file.size > 6 * 1024 * 1024) throw new Error("规则集文件不能超过 6 MiB。");
      setEntries(parseRuleSetFile(kind, await file.text()));
      setFileName(file.name);
      setError("");
    } catch (caught) {
      setEntries([]);
      setFileName("");
      setError(caught instanceof Error ? caught.message : "规则集文件无法读取。");
    }
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!entries.length) { setError("请先选择包含规则的文件。"); return; }
    setBusy(true); setError("");
    const result = await mutateCatalog<GeoRuleSetRecord>("/api/v1/admin/routing-rule-sets", "POST", { kind, code: code.trim(), name: name.trim(), version: version.trim(), source: source.trim(), entries }, csrfToken());
    setBusy(false);
    if (result.kind === "error") { setError(result.message); return; }
    setCode(""); setName(""); setVersion(""); setSource(""); setEntries([]); setFileName("");
    onRefresh();
  }

  async function toggle(item: GeoRuleSetRecord) {
    setToggleID(item.id); setError("");
    const result = await mutateCatalog<GeoRuleSetRecord>(`/api/v1/admin/routing-rule-sets/${item.id}`, "PATCH", { enabled: !item.enabled }, csrfToken());
    setToggleID("");
    if (result.kind === "error") setError(result.message); else onRefresh();
  }

  return <AdminSection title="GeoSite / GeoIP 规则集" description="上传管理员审核过的版本化规则集；启用新版本会自动停用同代码旧版本。" action={<Database size={18} />}>
    {canWrite && <form className="admin-geo-form" onSubmit={(event) => void submit(event)}>
      <div><label htmlFor="geo-kind">类型</label><select id="geo-kind" value={kind} onChange={(event) => { setKind(event.target.value as GeoRuleSetKind); setEntries([]); setFileName(""); }}><option value="geosite">GeoSite 域名</option><option value="geoip">GeoIP CIDR</option></select></div>
      <div><label htmlFor="geo-code">代码</label><input id="geo-code" required maxLength={64} value={code} onChange={(event) => setCode(event.target.value)} placeholder="例如 ai 或 cn" /></div>
      <div><label htmlFor="geo-name">名称</label><input id="geo-name" required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} placeholder="规则集名称" /></div>
      <div><label htmlFor="geo-version">版本</label><input id="geo-version" required maxLength={64} value={version} onChange={(event) => setVersion(event.target.value)} placeholder="例如 2026.09" /></div>
      <div><label htmlFor="geo-source">来源</label><input id="geo-source" required maxLength={500} value={source} onChange={(event) => setSource(event.target.value)} placeholder="来源 URL 或审核记录" /></div>
      <div><label htmlFor="geo-file">规则文件</label><input id="geo-file" type="file" accept=".txt,.list,.conf" onChange={(event) => void chooseFile(event)} /><small>{fileName ? `${fileName} · ${entries.length} 条` : "每行一条；# 开头为注释"}</small></div>
      {error && <div className="auth-error" role="alert">{error}</div>}
      <button className="primary-button" type="submit" disabled={busy}><Upload size={15} />{busy ? "正在上传…" : "上传并启用版本"}</button>
    </form>}
    <div className="admin-table">{sets.map((item) => <div className="admin-row" key={item.id}><strong>{item.kind === "geosite" ? "GeoSite" : "GeoIP"} · {item.code} · {item.version}</strong><span>{item.entry_count} 条 · SHA {item.sha256.slice(0, 12)}…</span><em>{item.enabled ? "当前启用" : "已停用"}</em>{canWrite && <button className="refresh-button admin-status-toggle" type="button" disabled={toggleID !== ""} onClick={() => void toggle(item)}>{toggleID === item.id ? "处理中…" : item.enabled ? "停用" : "启用"}</button>}</div>)}</div>
  </AdminSection>
}
