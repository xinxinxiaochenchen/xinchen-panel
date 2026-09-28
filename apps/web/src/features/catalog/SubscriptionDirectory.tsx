import { useEffect, useState, type FormEvent } from 'react'
import { Copy, Eye, KeyRound, Layers3, Pencil, Plus, RefreshCw, RotateCw, SlidersHorizontal, Trash2 } from 'lucide-react'
import type { Section } from '../../app/sections'
import { loadAllCatalogPages, mutateCatalog, type ProxyAccessRecord, type SubscriptionCreateResult, type SubscriptionRecord } from '../../lib/catalog'
import type { User } from '../../lib/dashboard'
import { csrfToken } from './CreateLine'
import { copyText } from '../../lib/clipboard'
import { ProxyAccessPanel } from './ProxyAccessPanel'
import { subscriptionFormats, subscriptionPreviewPath, subscriptionTokenPath, subscriptionURLPath, type SubscriptionFormat } from './subscriptionFormats'
import { subscriptionDraftFromRecord, subscriptionDraftPayload } from './subscriptionDraft'

type State = 'loading' | 'ready' | 'empty' | 'error'

function SubscriptionForm({ accesses, editing, onSaved, onEditCleared }: { accesses: ProxyAccessRecord[]; editing: SubscriptionRecord | null; onSaved: () => void; onEditCleared: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [template, setTemplate] = useState('{region} · {name}')
  const [selected, setSelected] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    if (!editing) return
    const draft = subscriptionDraftFromRecord(editing)
    setName(draft.name); setTemplate(draft.template); setSelected(draft.selected); setError(''); setOpen(true)
  }, [editing])
  function toggle(id: string) { setSelected((current) => current.includes(id) ? current.filter((value) => value !== id) : [...current, id]) }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    let payload: ReturnType<typeof subscriptionDraftPayload>
    try { payload = subscriptionDraftPayload({ name, template, selected }) } catch (cause) { setError(cause instanceof Error ? cause.message : '订阅配置无效。'); return }
    setBusy(true); setError('')
    const result = editing
      ? await mutateCatalog<SubscriptionRecord>(`/api/v1/subscriptions/${editing.id}`, 'PATCH', payload, csrfToken())
      : await mutateCatalog<SubscriptionCreateResult>('/api/v1/subscriptions', 'POST', payload, csrfToken())
    setBusy(false)
    if (result.kind === 'error') { setError(result.message); return }
    setOpen(false); setName(''); setSelected([]); if (editing) onEditCleared(); onSaved()
  }
  function close() { setOpen(false); setError(''); if (editing) onEditCleared() }
  return <>
    {!editing && <button className="primary-button catalog-create-button" type="button" onClick={() => { setName(''); setTemplate('{region} · {name}'); setSelected([]); setError(''); setOpen(true) }}><Plus size={16} />创建订阅</button>}
    {open && <div className="catalog-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) close() }}><form className="catalog-dialog" onSubmit={(event) => void submit(event)} aria-label={editing ? '编辑订阅' : '创建订阅'}>
      <div className="catalog-dialog-head"><span className="section-overline">{editing ? 'EDIT SUBSCRIPTION' : 'NEW SUBSCRIPTION'}</span><h2>{editing ? '编辑订阅配置' : '创建订阅配置'}</h2><p>选择已授权的代理连接，生成客户端订阅配置。</p></div>
      <label htmlFor="subscription-name">订阅名称</label><input id="subscription-name" required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} placeholder="例如 我的电脑" />
      <label htmlFor="subscription-template">客户端名称模板</label><input id="subscription-template" maxLength={200} value={template} onChange={(event) => setTemplate(event.target.value)} />
      <span className="catalog-form-label">代理连接</span><div className="catalog-check-list">{accesses.filter((item) => item.enabled || selected.includes(item.id)).map((item) => <label key={item.id} className="catalog-check"><input type="checkbox" checked={selected.includes(item.id)} onChange={() => toggle(item.id)} /><span><strong>{item.name}</strong><small>{item.apply_status} · {item.line_id.slice(0, 8)}{item.enabled ? '' : ' · 已停用'}</small></span></label>)}{selected.filter((id) => !accesses.some((item) => item.id === id)).map((id) => <label key={id} className="catalog-check"><input type="checkbox" checked onChange={() => toggle(id)} /><span><strong>不可用连接 {id.slice(0, 8)}</strong><small>取消勾选以移除</small></span></label>)}{accesses.length === 0 && selected.length === 0 && <span className="catalog-form-note">暂无代理连接，请先创建代理连接。</span>}</div>
      {error && <div className="auth-error" role="alert">{error}</div>}
      <div className="catalog-form-actions"><button type="button" className="refresh-button" onClick={close}>取消</button><button className="primary-button" type="submit" disabled={busy}>{busy ? '正在保存…' : editing ? '保存订阅' : '创建订阅'}<Plus size={16} /></button></div>
    </form></div>}
  </>
}

export function SubscriptionDirectory({ section, user }: { section: Section; user: User }) {
  const [state, setState] = useState<State>('loading')
  const [items, setItems] = useState<SubscriptionRecord[]>([])
  const [accesses, setAccesses] = useState<ProxyAccessRecord[]>([])
  const [lines, setLines] = useState<import('../../lib/catalog').LineRecord[]>([])
  const [generation, setGeneration] = useState(0)
  const [error, setError] = useState('')
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  const [format, setFormat] = useState<SubscriptionFormat>('mihomo')
  const [editing, setEditing] = useState<SubscriptionRecord | null>(null)
  useEffect(() => {
    let active = true
    setState('loading'); setError('')
    Promise.all([loadAllCatalogPages<SubscriptionRecord>('/api/v1/subscriptions'), loadAllCatalogPages<ProxyAccessRecord>('/api/v1/proxy-accesses'), loadAllCatalogPages<import('../../lib/catalog').LineRecord>('/api/v1/lines')]).then(([subscriptions, accessPage, linePage]) => {
      if (!active) return
      if (subscriptions.kind === 'ready') { setItems(subscriptions.data); setState(subscriptions.data.length ? 'ready' : 'empty') }
      else if (subscriptions.kind === 'empty') setState('empty')
      else if (subscriptions.kind === 'error') { setState('error'); setError(subscriptions.message) }
      if (accessPage.kind === 'ready') setAccesses(accessPage.data)
      if (linePage.kind === 'ready') setLines(linePage.data)
    })
    return () => { active = false }
  }, [user.id, generation])
  async function toggle(item: SubscriptionRecord) { const result = await mutateCatalog<SubscriptionRecord>(`/api/v1/subscriptions/${item.id}`, 'PATCH', { enabled: !item.enabled }, csrfToken()); if (result.kind === 'error') setError(result.message); else setGeneration((value) => value + 1) }
  async function remove(item: SubscriptionRecord) {
    if (!window.confirm(`删除「${item.name}」？订阅地址会立即失效。`)) return
    const result = await mutateCatalog<void>(`/api/v1/subscriptions/${item.id}`, 'DELETE', null, csrfToken())
    if (result.kind === 'error') setError(result.message); else setGeneration((value) => value + 1)
  }
  async function rotate(item: SubscriptionRecord) {
    if (!window.confirm(`重置「${item.name}」的 Token？旧订阅地址会立即失效。`)) return
    const result = await mutateCatalog<{ token: string }>(`/api/v1/subscriptions/${item.id}/token-rotation`, 'POST', {}, csrfToken())
    if (result.kind === 'error') setError(result.message)
    else if (result.data?.token) setRevealed((current) => ({ ...current, [item.id]: subscriptionTokenPath(result.data.token, format) }))
  }
  async function copyURL(item: SubscriptionRecord) {
    const path = revealed[item.id]
    if (!path) return
    if (!await copyText(`${window.location.origin}${path}`)) setError('复制失败，请手动复制订阅地址。')
    else setError('')
  }
  async function reveal(item: SubscriptionRecord) {
    try {
      const response = await fetch(subscriptionURLPath(item.id, format), { credentials: 'same-origin', cache: 'no-store' })
      const data = await response.json() as { path?: string; error?: { message?: string } }
      if (!response.ok) { setError(data.error?.message || `请求失败（${response.status}）`); return }
      if (!data.path?.startsWith('/sub/') || data.path.includes('//') || !data.path.endsWith(`/${format}`)) { setError('订阅地址格式无效。'); return }
      setRevealed((current) => ({ ...current, [item.id]: data.path! }))
    } catch { setError('订阅地址暂不可用。') }
  }
  return <div className="catalog-page"><div className="catalog-title"><div><span className="section-overline">{section.eyebrow}</span><h1>{section.title}</h1><p>{section.description}</p></div><div className="catalog-title-actions"><label className="catalog-format-picker" htmlFor="subscription-format">订阅格式 <select id="subscription-format" value={format} onChange={(event) => { setFormat(event.target.value as SubscriptionFormat); setRevealed({}) }}>{subscriptionFormats.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label><button className="refresh-button" type="button" onClick={() => setGeneration((value) => value + 1)}><RefreshCw size={16} />刷新列表</button>{user.permissions.includes('subscriptions.write') && <SubscriptionForm accesses={accesses} editing={editing} onEditCleared={() => setEditing(null)} onSaved={() => setGeneration((value) => value + 1)} />}</div></div>
    {user.permissions.includes('proxy_accesses.read') && <ProxyAccessPanel accesses={accesses} lines={lines} onSaved={() => setGeneration((value) => value + 1)} canWrite={user.permissions.includes('proxy_accesses.write')} />}
    {state === 'loading' && <div className="catalog-state" role="status">正在加载订阅…</div>}{state === 'error' && <div className="catalog-state catalog-error">{error}</div>}{state === 'empty' && <div className="catalog-state"><Layers3 size={24} /><span>还没有订阅配置。</span></div>}
    {state === 'ready' && <div className="subscription-grid">{items.map((item) => <article className="catalog-card subscription-card" key={item.id}><div className="catalog-card-head"><span className="catalog-icon"><Layers3 size={18} /></span><span className={`status-chip ${item.enabled ? 'status-online' : 'status-offline'}`}><i />{item.enabled ? '启用' : '停用'}</span></div><div className="catalog-card-name"><strong>{item.name}</strong><span>{item.proxy_access_ids.length} 个代理连接</span></div>{revealed[item.id]?.endsWith(`/${format}`) && <div className="subscription-url"><code>{revealed[item.id]}</code><button type="button" aria-label="复制订阅地址" onClick={() => void copyURL(item)}><Copy size={14} /></button></div>}<div className="catalog-card-foot"><span><SlidersHorizontal size={13} /> {item.name_template}</span><span>{new Date(item.updated_at).toLocaleDateString('zh-CN')}</span></div><div className="subscription-actions"><button type="button" onClick={() => void reveal(item)}><Eye size={14} />地址</button><a href={subscriptionPreviewPath(item.id, format)} target="_blank" rel="noopener noreferrer"><Eye size={14} />预览</a>{user.permissions.includes('subscriptions.write') && <><button type="button" onClick={() => setEditing(item)}><Pencil size={14} />编辑</button><button type="button" onClick={() => void rotate(item)}><RotateCw size={14} />重置 Token</button><button type="button" onClick={() => void toggle(item)}><KeyRound size={14} />{item.enabled ? '停用' : '启用'}</button><button type="button" onClick={() => void remove(item)}><Trash2 size={14} />删除</button></>}</div></article>)}</div>}
    {error && state === 'ready' && <p className="catalog-page-error" role="alert">{error}</p>}
  </div>
}
