import { useState, type FormEvent } from 'react'
import { Eye, KeyRound, Plus, RotateCw, ShieldCheck } from 'lucide-react'
import { mutateCatalog, type LineRecord, type ProxyAccessRecord } from '../../lib/catalog'
import { csrfToken } from './CreateLine'

export function ProxyAccessPanel({ accesses, lines, onSaved, canWrite }: { accesses: ProxyAccessRecord[]; lines: LineRecord[]; onSaved: () => void; canWrite: boolean }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [line, setLine] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [credential, setCredential] = useState<Record<string, string>>({})
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); setError('')
    const result = await mutateCatalog<{ access: ProxyAccessRecord; credential: string }>('/api/v1/proxy-accesses', 'POST', { name: name.trim(), line_id: line }, csrfToken())
    setBusy(false)
    if (result.kind === 'error') { setError(result.message); return }
    setCredential((current) => ({ ...current, [result.data.access.id]: result.data.credential }))
    setOpen(false); setName(''); setLine(''); onSaved()
  }
  async function toggle(access: ProxyAccessRecord) {
    const result = await mutateCatalog<ProxyAccessRecord>(`/api/v1/proxy-accesses/${access.id}`, 'PATCH', { enabled: !access.enabled }, csrfToken())
    if (result.kind === 'error') setError(result.message); else onSaved()
  }
  async function rotate(access: ProxyAccessRecord) {
    if (!window.confirm(`轮换「${access.name}」的凭据？旧客户端配置将在新版本应用后失效。`)) return
    const result = await mutateCatalog<{ credential: string }>(`/api/v1/proxy-accesses/${access.id}/credential-rotation`, 'POST', {}, csrfToken())
    if (result.kind === 'error') setError(result.message)
    else { setCredential((current) => ({ ...current, [access.id]: result.data.credential })); onSaved() }
  }
  async function reveal(access: ProxyAccessRecord) {
    try {
      const response = await fetch(`/api/v1/proxy-accesses/${access.id}/credential`, { credentials: 'same-origin', cache: 'no-store' })
      const data = await response.json() as { credential?: string; error?: { message?: string } }
      if (!response.ok) setError(data.error?.message || `请求失败（${response.status}）`)
      else if (data.credential) setCredential((current) => ({ ...current, [access.id]: data.credential! }))
    } catch { setError('凭据暂不可用。') }
  }
  return <section className="proxy-panel" aria-labelledby="proxy-panel-title">
    <div className="proxy-panel-head"><div><span className="section-overline">PROXY ACCESS</span><h2 id="proxy-panel-title">代理连接</h2><p>每条连接绑定一条授权线路，订阅从这些连接中选择出口。</p></div>{canWrite && <button className="primary-button catalog-create-button" type="button" onClick={() => setOpen(true)}><Plus size={16} />创建连接</button>}</div>
    {accesses.length === 0 ? <div className="catalog-state proxy-empty"><ShieldCheck size={24} /><span>还没有代理连接。</span></div> : <div className="proxy-access-list">{accesses.map((access) => <div className="proxy-access-row" key={access.id}><span className="catalog-icon"><ShieldCheck size={18} /></span><div className="proxy-access-name"><strong>{access.name}</strong><small>线路 {access.line_id.slice(0, 8)} · {access.apply_status}</small>{credential[access.id] && <code>{credential[access.id]}</code>}</div><span className={`status-chip ${access.enabled ? 'status-online' : 'status-offline'}`}><i />{access.enabled ? '启用' : '停用'}</span><div className="proxy-access-actions"><button type="button" onClick={() => void reveal(access)}><Eye size={14} />查看</button>{canWrite && <><button type="button" onClick={() => void rotate(access)}><RotateCw size={14} />轮换</button><button type="button" onClick={() => void toggle(access)}><KeyRound size={14} />{access.enabled ? '停用' : '启用'}</button></>}</div></div>)}</div>}
    {error && <p className="catalog-page-error" role="alert">{error}</p>}
    {open && <div className="catalog-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setOpen(false) }}><form className="catalog-dialog" onSubmit={(event) => void submit(event)} aria-label="创建代理连接"><div className="catalog-dialog-head"><span className="section-overline">NEW ACCESS</span><h2>创建代理连接</h2><p>选择已授权的单跳线路；Agent 应用配置后，订阅才会导出此连接。</p></div><label htmlFor="proxy-name">连接名称</label><input id="proxy-name" required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} placeholder="例如 日本出口" /><label htmlFor="proxy-line">使用线路</label><select id="proxy-line" required value={line} onChange={(event) => setLine(event.target.value)}><option value="">请选择线路</option>{lines.filter((item) => item.enabled).map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select>{error && <div className="auth-error" role="alert">{error}</div>}<div className="catalog-form-actions"><button className="refresh-button" type="button" onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={busy}>{busy ? '正在创建…' : '创建连接'}<Plus size={16} /></button></div></form></div>}
  </section>
}
