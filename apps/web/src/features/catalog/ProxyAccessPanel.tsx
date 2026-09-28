import { useState, type FormEvent } from 'react'
import { Eye, KeyRound, Pencil, Plus, RotateCw, ShieldCheck, Trash2 } from 'lucide-react'
import { mutateCatalog, type LineRecord, type ProxyAccessRecord } from '../../lib/catalog'
import { csrfToken } from './CreateLine'
import { proxyAccessDraftFromRecord, proxyAccessPayload, type ProxyLineDraft } from './proxyAccessDraft'

type EditorProps = {
  lines: LineRecord[]
  selected: ProxyLineDraft[]
  onToggle: (lineID: string) => void
  onUpdate: (lineID: string, field: 'priority' | 'weight', value: number) => void
}

function ProxyLinePicker({ lines, selected, onToggle, onUpdate }: EditorProps) {
  const visible = lines.filter((line) => line.enabled || selected.some((item) => item.lineID === line.id))
  const unlisted = selected.filter((item) => !lines.some((line) => line.id === item.lineID))
  return <div className="catalog-check-list">
    {visible.map((line) => {
      const index = selected.findIndex((item) => item.lineID === line.id)
      const option = selected[index]
      return <div className="catalog-check proxy-line-choice" key={line.id}>
        <label><input type="checkbox" checked={Boolean(option)} onChange={() => onToggle(line.id)} />
          <span><strong>{line.name}</strong><small>{line.hops.length} 跳{option && ` · 候选 ${index + 1}`}{!line.enabled && ' · 已停用'}</small></span>
        </label>
        {option && <div className="catalog-form-row proxy-line-options">
          <label>优先级<input type="number" min={0} max={1000} value={option.priority} onChange={(event) => onUpdate(line.id, 'priority', Number(event.target.value))} /></label>
          <label>权重<input type="number" min={1} max={100} value={option.weight} onChange={(event) => onUpdate(line.id, 'weight', Number(event.target.value))} /></label>
        </div>}
      </div>
    })}
    {unlisted.map((option) => <label className="catalog-check" key={option.lineID}>
      <input type="checkbox" checked onChange={() => onToggle(option.lineID)} />
      <span><strong>不可用线路 {option.lineID.slice(0, 8)}</strong><small>取消勾选以移除</small></span>
    </label>)}
    {visible.length === 0 && unlisted.length === 0 && <span className="catalog-form-note">暂无可用线路，请先创建或申请线路。</span>}
  </div>
}

export function ProxyAccessPanel({ accesses, lines, onSaved, canWrite }: { accesses: ProxyAccessRecord[]; lines: LineRecord[]; onSaved: () => void; canWrite: boolean }) {
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<ProxyAccessRecord | null>(null)
  const [name, setName] = useState('')
  const [selectedLines, setSelectedLines] = useState<ProxyLineDraft[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [credential, setCredential] = useState<Record<string, string>>({})

  function openCreate() {
    setEditing(null); setName(''); setSelectedLines([]); setError(''); setOpen(true)
  }
  function openEdit(access: ProxyAccessRecord) {
    setEditing(access); setName(access.name); setSelectedLines(proxyAccessDraftFromRecord(access)); setError(''); setOpen(true)
  }
  function closeEditor() {
    setOpen(false); setEditing(null); setError('')
  }
  function toggleLine(lineID: string) {
    setSelectedLines((current) => current.some((line) => line.lineID === lineID)
      ? current.filter((line) => line.lineID !== lineID)
      : [...current, { lineID, priority: 100, weight: 1 }])
  }
  function updateLine(lineID: string, field: 'priority' | 'weight', value: number) {
    setSelectedLines((current) => current.map((line) => line.lineID === lineID ? { ...line, [field]: value } : line))
  }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    let payload: ReturnType<typeof proxyAccessPayload>
    try { payload = proxyAccessPayload(name, selectedLines) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '线路选择无效。'); return }
    setBusy(true); setError('')
    if (editing) {
      const result = await mutateCatalog<ProxyAccessRecord>(`/api/v1/proxy-accesses/${editing.id}`, 'PATCH', payload, csrfToken())
      setBusy(false)
      if (result.kind === 'error') { setError(result.message); return }
    } else {
      const result = await mutateCatalog<{ access: ProxyAccessRecord; credential: string }>('/api/v1/proxy-accesses', 'POST', payload, csrfToken())
      setBusy(false)
      if (result.kind === 'error') { setError(result.message); return }
      setCredential((current) => ({ ...current, [result.data.access.id]: result.data.credential }))
    }
    closeEditor(); setName(''); setSelectedLines([]); onSaved()
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
  async function remove(access: ProxyAccessRecord) {
    if (!window.confirm(`删除代理连接「${access.name}」？现有客户端凭据会立即失效。`)) return
    const result = await mutateCatalog<void>(`/api/v1/proxy-accesses/${access.id}`, 'DELETE', null, csrfToken())
    if (result.kind === 'error') setError(result.message); else onSaved()
  }

  return <section className="proxy-panel" aria-labelledby="proxy-panel-title">
    <div className="proxy-panel-head"><div><span className="section-overline">PROXY ACCESS</span><h2 id="proxy-panel-title">代理连接</h2><p>可绑定多条授权线路；Agent 会按优先级和权重为每次新连接选择出口。</p></div>
      {canWrite && <button className="primary-button catalog-create-button" type="button" onClick={openCreate}><Plus size={16} />创建连接</button>}
    </div>
    {accesses.length === 0 ? <div className="catalog-state proxy-empty"><ShieldCheck size={24} /><span>还没有代理连接。</span></div>
      : <div className="proxy-access-list">{accesses.map((access) => <div className="proxy-access-row" key={access.id}>
        <span className="catalog-icon"><ShieldCheck size={18} /></span>
        <div className="proxy-access-name"><strong>{access.name}</strong><small>{access.line_ids?.length && access.line_ids.length > 1 ? `${access.line_ids.length} 条候选线路` : `线路 ${(access.line_ids?.[0] ?? access.line_id).slice(0, 8)}`} · {access.apply_status}</small>{credential[access.id] && <code>{credential[access.id]}</code>}</div>
        <span className={`status-chip ${access.enabled ? 'status-online' : 'status-offline'}`}><i />{access.enabled ? '启用' : '停用'}</span>
        <div className="proxy-access-actions"><button type="button" onClick={() => void reveal(access)}><Eye size={14} />查看</button>{canWrite && <>
          <button type="button" onClick={() => openEdit(access)}><Pencil size={14} />编辑线路</button>
          <button type="button" onClick={() => void rotate(access)}><RotateCw size={14} />轮换</button>
          <button type="button" onClick={() => void toggle(access)}><KeyRound size={14} />{access.enabled ? '停用' : '启用'}</button>
          <button type="button" onClick={() => void remove(access)}><Trash2 size={14} />删除</button>
        </>}</div>
      </div>)}</div>}
    {error && !open && <p className="catalog-page-error" role="alert">{error}</p>}
    {open && <div className="catalog-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) closeEditor() }}>
      <form className="catalog-dialog" onSubmit={(event) => void submit(event)} aria-label={editing ? '编辑代理连接线路' : '创建代理连接'}>
        <div className="catalog-dialog-head"><span className="section-overline">{editing ? 'EDIT ACCESS' : 'NEW ACCESS'}</span><h2>{editing ? '编辑代理连接' : '创建代理连接'}</h2><p>选择一条或多条已授权线路；候选必须共用入口节点，套餐会限制数量。</p></div>
        <label htmlFor="proxy-name">连接名称</label><input id="proxy-name" required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} placeholder="例如 日本出口" />
        <span className="catalog-form-label">候选线路</span>
        <ProxyLinePicker lines={lines} selected={selectedLines} onToggle={toggleLine} onUpdate={updateLine} />
        {error && <div className="auth-error" role="alert">{error}</div>}
        <div className="catalog-form-actions"><button className="refresh-button" type="button" onClick={closeEditor}>取消</button><button className="primary-button" type="submit" disabled={busy}>{busy ? '正在保存…' : editing ? '保存线路' : '创建连接'}<Plus size={16} /></button></div>
      </form>
    </div>}
  </section>
}
