import { useEffect, useState, type FormEvent } from 'react'
import { Copy, Eye, KeyRound, Layers3, Plus, RefreshCw, RotateCw, SlidersHorizontal } from 'lucide-react'
import type { Section } from '../../app/sections'
import { loadAllCatalogPages, mutateCatalog, type ProxyAccessRecord, type RoutingProfileRecord, type SubscriptionCreateResult, type SubscriptionRecord } from '../../lib/catalog'
import type { User } from '../../lib/dashboard'
import { csrfToken } from './CreateLine'
import { ProxyAccessPanel } from './ProxyAccessPanel'

type State = 'loading' | 'ready' | 'empty' | 'error'

function SubscriptionForm({ accesses, profiles, onSaved }: { accesses: ProxyAccessRecord[]; profiles: RoutingProfileRecord[]; onSaved: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [template, setTemplate] = useState('{region} · {name}')
  const [selected, setSelected] = useState<string[]>([])
  const [profile, setProfile] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  function toggle(id: string) { setSelected((current) => current.includes(id) ? current.filter((value) => value !== id) : [...current, id]) }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!selected.length) { setError('至少选择一个代理连接。'); return }
    setBusy(true); setError('')
    const result = await mutateCatalog<SubscriptionCreateResult>('/api/v1/subscriptions', 'POST', { name: name.trim(), name_template: template, proxy_access_ids: selected, routing_profile_id: profile || null }, csrfToken())
    setBusy(false)
    if (result.kind === 'error') { setError(result.message); return }
    setOpen(false); setName(''); setSelected([]); setProfile(''); onSaved()
  }
  return <>
    <button className="primary-button catalog-create-button" type="button" onClick={() => setOpen(true)}><Plus size={16} />创建订阅</button>
    {open && <div className="catalog-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setOpen(false) }}><form className="catalog-dialog" onSubmit={(event) => void submit(event)} aria-label="创建订阅">
      <div className="catalog-dialog-head"><span className="section-overline">NEW SUBSCRIPTION</span><h2>创建订阅配置</h2><p>选择已授权的代理连接，可选绑定一个启用中的分流 Profile。</p></div>
      <label htmlFor="subscription-name">订阅名称</label><input id="subscription-name" required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} placeholder="例如 我的电脑" />
      <label htmlFor="subscription-template">客户端名称模板</label><input id="subscription-template" maxLength={200} value={template} onChange={(event) => setTemplate(event.target.value)} />
      <label htmlFor="subscription-profile">分流 Profile（可选）</label><select id="subscription-profile" value={profile} onChange={(event) => setProfile(event.target.value)}><option value="">默认代理线路</option>{profiles.filter((item) => item.enabled).map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select>
      <span className="catalog-form-label">代理连接</span><div className="catalog-check-list">{accesses.filter((item) => item.enabled).map((item) => <label key={item.id} className="catalog-check"><input type="checkbox" checked={selected.includes(item.id)} onChange={() => toggle(item.id)} /><span><strong>{item.name}</strong><small>{item.apply_status} · {item.line_id.slice(0, 8)}</small></span></label>)}{accesses.length === 0 && <span className="catalog-form-note">暂无代理连接，请先创建代理连接。</span>}</div>
      {error && <div className="auth-error" role="alert">{error}</div>}
      <div className="catalog-form-actions"><button type="button" className="refresh-button" onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={busy}>{busy ? '正在创建…' : '创建订阅'}<Plus size={16} /></button></div>
    </form></div>}
  </>
}

export function SubscriptionDirectory({ section, user }: { section: Section; user: User }) {
  const [state, setState] = useState<State>('loading')
  const [items, setItems] = useState<SubscriptionRecord[]>([])
  const [accesses, setAccesses] = useState<ProxyAccessRecord[]>([])
  const [profiles, setProfiles] = useState<RoutingProfileRecord[]>([])
  const [lines, setLines] = useState<import('../../lib/catalog').LineRecord[]>([])
  const [generation, setGeneration] = useState(0)
  const [error, setError] = useState('')
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  useEffect(() => {
    let active = true
    setState('loading'); setError('')
    Promise.all([loadAllCatalogPages<SubscriptionRecord>('/api/v1/subscriptions'), loadAllCatalogPages<ProxyAccessRecord>('/api/v1/proxy-accesses'), loadAllCatalogPages<RoutingProfileRecord>('/api/v1/routing-profiles'), loadAllCatalogPages<import('../../lib/catalog').LineRecord>('/api/v1/lines')]).then(([subscriptions, accessPage, profilePage, linePage]) => {
      if (!active) return
      if (subscriptions.kind === 'ready') { setItems(subscriptions.data); setState(subscriptions.data.length ? 'ready' : 'empty') }
      else if (subscriptions.kind === 'empty') setState('empty')
      else if (subscriptions.kind === 'error') { setState('error'); setError(subscriptions.message) }
      if (accessPage.kind === 'ready') setAccesses(accessPage.data)
      if (profilePage.kind === 'ready') setProfiles(profilePage.data)
      if (linePage.kind === 'ready') setLines(linePage.data)
    })
    return () => { active = false }
  }, [user.id, generation])
  async function toggle(item: SubscriptionRecord) { const result = await mutateCatalog<SubscriptionRecord>(`/api/v1/subscriptions/${item.id}`, 'PATCH', { enabled: !item.enabled }, csrfToken()); if (result.kind === 'error') setError(result.message); else setGeneration((value) => value + 1) }
  async function rotate(item: SubscriptionRecord) {
    if (!window.confirm(`重置「${item.name}」的 Token？旧订阅地址会立即失效。`)) return
    const result = await mutateCatalog<{ token: string }>(`/api/v1/subscriptions/${item.id}/token-rotation`, 'POST', {}, csrfToken())
    if (result.kind === 'error') setError(result.message)
    else if (result.data?.token) setRevealed((current) => ({ ...current, [item.id]: `/sub/${result.data.token}/mihomo` }))
  }
  async function reveal(item: SubscriptionRecord) {
    try {
      const response = await fetch(`/api/v1/subscriptions/${item.id}/url?format=mihomo`, { credentials: 'same-origin', cache: 'no-store' })
      const data = await response.json() as { path?: string; error?: { message?: string } }
      if (!response.ok) { setError(data.error?.message || `请求失败（${response.status}）`); return }
      if (!data.path?.startsWith('/sub/') || data.path.includes('//')) { setError('订阅地址格式无效。'); return }
      setRevealed((current) => ({ ...current, [item.id]: data.path! }))
    } catch { setError('订阅地址暂不可用。') }
  }
  return <div className="catalog-page"><div className="catalog-title"><div><span className="section-overline">{section.eyebrow}</span><h1>{section.title}</h1><p>{section.description}</p></div><div className="catalog-title-actions"><button className="refresh-button" type="button" onClick={() => setGeneration((value) => value + 1)}><RefreshCw size={16} />刷新列表</button>{user.permissions.includes('subscriptions.write') && <SubscriptionForm accesses={accesses} profiles={profiles} onSaved={() => setGeneration((value) => value + 1)} />}</div></div>
    {user.permissions.includes('proxy_accesses.read') && <ProxyAccessPanel accesses={accesses} lines={lines} onSaved={() => setGeneration((value) => value + 1)} canWrite={user.permissions.includes('proxy_accesses.write')} />}
    {state === 'loading' && <div className="catalog-state" role="status">正在加载订阅…</div>}{state === 'error' && <div className="catalog-state catalog-error">{error}</div>}{state === 'empty' && <div className="catalog-state"><Layers3 size={24} /><span>还没有订阅配置。</span></div>}
    {state === 'ready' && <div className="subscription-grid">{items.map((item) => <article className="catalog-card subscription-card" key={item.id}><div className="catalog-card-head"><span className="catalog-icon"><Layers3 size={18} /></span><span className={`status-chip ${item.enabled ? 'status-online' : 'status-offline'}`}><i />{item.enabled ? '启用' : '停用'}</span></div><div className="catalog-card-name"><strong>{item.name}</strong><span>{item.proxy_access_ids.length} 个代理连接 · {item.routing_profile_id ? '已绑定分流' : '默认路由'}</span></div>{revealed[item.id] && <div className="subscription-url"><code>{revealed[item.id]}</code><button type="button" aria-label="复制订阅地址" onClick={() => void navigator.clipboard?.writeText(`${window.location.origin}${revealed[item.id]}`)}><Copy size={14} /></button></div>}<div className="catalog-card-foot"><span><SlidersHorizontal size={13} /> {item.name_template}</span><span>{new Date(item.updated_at).toLocaleDateString('zh-CN')}</span></div><div className="subscription-actions"><button type="button" onClick={() => void reveal(item)}><Eye size={14} />地址</button>{user.permissions.includes('subscriptions.write') && <><button type="button" onClick={() => void rotate(item)}><RotateCw size={14} />重置 Token</button><button type="button" onClick={() => void toggle(item)}><KeyRound size={14} />{item.enabled ? '停用' : '启用'}</button></>}</div></article>)}</div>}
    {error && state === 'ready' && <p className="catalog-page-error" role="alert">{error}</p>}
  </div>
}
