import { useEffect, useState, type FormEvent } from 'react'
import { ArrowRight, ListFilter, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import type { Section } from '../../app/sections'
import { loadAllCatalogPages, mutateCatalog, type LineRecord, type RoutingProfileRecord } from '../../lib/catalog'
import type { User } from '../../lib/dashboard'
import { csrfToken } from './CreateLine'
import { profileDraftFromRecord, profileDraftPayload, ruleDraftFromRecord, ruleDraftPayload, type RoutingAction } from './routingDraft'

type RuleRecord = { id: string; profile_id: string; priority: number; match_type: string; match_value: string; action: string; line_id: string | null; enabled: boolean; created_at: string; updated_at: string }

function ProfileForm({ lines, editing, onSaved }: { lines: LineRecord[]; editing?: RoutingProfileRecord; onSaved: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [fallback, setFallback] = useState<'direct' | 'block' | 'line'>('direct')
  const [line, setLine] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  function openEditor() {
    const draft = editing ? profileDraftFromRecord(editing) : { name: '', fallback: 'direct' as RoutingAction, line: '' }
    setName(draft.name); setFallback(draft.fallback); setLine(draft.line); setError(''); setOpen(true)
  }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    let payload: ReturnType<typeof profileDraftPayload>
    try { payload = profileDraftPayload({ name, fallback, line }) } catch (cause) { setError(cause instanceof Error ? cause.message : 'Profile 配置无效。'); return }
    setBusy(true); setError('')
    const result = await mutateCatalog<RoutingProfileRecord>(editing ? `/api/v1/routing-profiles/${editing.id}` : '/api/v1/routing-profiles', editing ? 'PATCH' : 'POST', payload, csrfToken())
    setBusy(false)
    if (result.kind === 'error') { setError(result.message); return }
    setOpen(false); onSaved()
  }
  return <>
    <button className={editing ? 'catalog-text-button' : 'primary-button catalog-create-button'} type="button" onClick={openEditor}>{editing ? <Pencil size={14} /> : <Plus size={16} />}{editing ? '编辑 Profile' : '创建 Profile'}</button>
    {open && <div className="catalog-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setOpen(false) }}><form className="catalog-dialog" onSubmit={(event) => void submit(event)} aria-label={editing ? '编辑分流 Profile' : '创建分流 Profile'}>
      <div className="catalog-dialog-head"><span className="section-overline">{editing ? 'EDIT ROUTING PROFILE' : 'NEW ROUTING PROFILE'}</span><h2>{editing ? '编辑分流 Profile' : '创建分流 Profile'}</h2><p>设置未命中规则时的默认动作。</p></div>
      <label htmlFor="profile-name">Profile 名称</label><input id="profile-name" required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} placeholder="例如 日常分流" />
      <label htmlFor="profile-fallback">默认动作</label><select id="profile-fallback" value={fallback} onChange={(event) => setFallback(event.target.value as typeof fallback)}><option value="direct">直连</option><option value="block">阻断</option><option value="line">指定线路</option></select>
      {fallback === 'line' && <><label htmlFor="profile-line">默认线路</label><select id="profile-line" required value={line} onChange={(event) => setLine(event.target.value)}><option value="">请选择线路</option>{lines.filter((item) => item.enabled || item.id === line).map((item) => <option key={item.id} value={item.id}>{item.name}{item.enabled ? '' : ' · 已停用'}</option>)}{line && !lines.some((item) => item.id === line) && <option value={line}>当前线路不可用 · {line.slice(0, 8)}</option>}</select></>}
      {error && <div className="auth-error" role="alert">{error}</div>}
      <div className="catalog-form-actions"><button className="refresh-button" type="button" onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={busy}>{busy ? '正在保存…' : editing ? '保存 Profile' : '创建 Profile'}<ArrowRight size={16} /></button></div>
    </form></div>}
  </>
}

function RuleForm({ profileID, lines, editing, onSaved }: { profileID: string; lines: LineRecord[]; editing?: RuleRecord; onSaved: () => void }) {
  const [open, setOpen] = useState(false)
  const [priority, setPriority] = useState(100)
  const [matchType, setMatchType] = useState('domain')
  const [value, setValue] = useState('')
  const [action, setAction] = useState<'direct' | 'block' | 'line'>('direct')
  const [line, setLine] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  function openEditor() {
    const draft = editing ? ruleDraftFromRecord(editing) : { priority: 100, matchType: 'domain', matchValue: '', action: 'direct' as RoutingAction, line: '' }
    setPriority(draft.priority); setMatchType(draft.matchType); setValue(draft.matchValue); setAction(draft.action); setLine(draft.line); setError(''); setOpen(true)
  }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    let payload: ReturnType<typeof ruleDraftPayload>
    try { payload = ruleDraftPayload({ priority, matchType, matchValue: value, action, line }, Boolean(editing)) } catch (cause) { setError(cause instanceof Error ? cause.message : '规则配置无效。'); return }
    setBusy(true); setError('')
    const result = await mutateCatalog<RuleRecord>(editing ? `/api/v1/routing-profiles/${profileID}/rules/${editing.id}` : `/api/v1/routing-profiles/${profileID}/rules`, editing ? 'PATCH' : 'POST', payload, csrfToken())
    setBusy(false)
    if (result.kind === 'error') { setError(result.message); return }
    setOpen(false); onSaved()
  }
  return <>
    <button className="catalog-text-button" type="button" onClick={openEditor}>{editing ? <Pencil size={14} /> : <Plus size={14} />}{editing ? '编辑' : '添加规则'}</button>
    {open && <div className="catalog-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setOpen(false) }}><form className="catalog-dialog" onSubmit={(event) => void submit(event)} aria-label={editing ? '编辑分流规则' : '添加分流规则'}>
      <div className="catalog-dialog-head"><span className="section-overline">{editing ? 'EDIT RULE' : 'NEW RULE'}</span><h2>{editing ? '编辑分流规则' : '添加分流规则'}</h2><p>规则按优先级从小到大匹配；GeoSite 使用管理员启用的同代码规则集。</p></div>
      <div className="catalog-form-row"><div><label htmlFor="rule-priority">优先级</label><input id="rule-priority" type="number" min={1} max={1000000} value={priority} onChange={(event) => setPriority(Number(event.target.value))} /></div><div><label htmlFor="rule-type">匹配类型</label><select id="rule-type" value={matchType} disabled={Boolean(editing)} onChange={(event) => setMatchType(event.target.value)}><option value="domain">域名</option><option value="domain_suffix">域名后缀</option><option value="ip">IP 地址</option><option value="cidr">CIDR</option><option value="geoip">GeoIP</option><option value="geosite">GeoSite</option></select></div></div>
      <label htmlFor="rule-value">匹配值</label><input id="rule-value" required maxLength={253} disabled={Boolean(editing)} value={value} onChange={(event) => setValue(event.target.value)} placeholder={matchType === 'domain' ? '例如 google.com' : matchType === 'cidr' ? '例如 10.0.0.0/8' : '输入匹配值'} />
      <label htmlFor="rule-action">动作</label><select id="rule-action" value={action} onChange={(event) => setAction(event.target.value as typeof action)}><option value="direct">直连</option><option value="block">阻断</option><option value="line">指定线路</option></select>
      {action === 'line' && <><label htmlFor="rule-line">目标线路</label><select id="rule-line" required value={line} onChange={(event) => setLine(event.target.value)}><option value="">请选择线路</option>{lines.filter((item) => item.enabled).map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></>}
      {error && <div className="auth-error" role="alert">{error}</div>}
      <div className="catalog-form-actions"><button className="refresh-button" type="button" onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={busy}>{busy ? '正在保存…' : editing ? '保存规则' : '添加规则'}<ArrowRight size={16} /></button></div>
    </form></div>}
  </>
}

export function RoutingDirectory({ section, user }: { section: Section; user: User }) {
  const [profiles, setProfiles] = useState<RoutingProfileRecord[]>([])
  const [lines, setLines] = useState<LineRecord[]>([])
  const [rules, setRules] = useState<Record<string, RuleRecord[]>>({})
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'error'>('loading')
  const [error, setError] = useState('')
  const [generation, setGeneration] = useState(0)
  const [expanded, setExpanded] = useState<string | null>(null)
  useEffect(() => {
    let active = true
    setState('loading'); setError('')
    Promise.all([loadAllCatalogPages<RoutingProfileRecord>('/api/v1/routing-profiles'), loadAllCatalogPages<LineRecord>('/api/v1/lines')]).then(([profilePage, linePage]) => {
      if (!active) return
      if (profilePage.kind === 'ready') { setProfiles(profilePage.data); setState(profilePage.data.length ? 'ready' : 'empty') } else if (profilePage.kind === 'empty') setState('empty'); else if (profilePage.kind === 'error') { setState('error'); setError(profilePage.message) }
      if (linePage.kind === 'ready') setLines(linePage.data)
    })
    return () => { active = false }
  }, [user.id, generation])
  async function reloadRules(id: string) {
    const page = await loadAllCatalogPages<RuleRecord>(`/api/v1/routing-profiles/${id}/rules`)
    if (page.kind === 'ready') setRules((current) => ({ ...current, [id]: page.data }))
    else if (page.kind === 'empty') setRules((current) => ({ ...current, [id]: [] }))
    else if (page.kind === 'error') setError(page.message)
  }
  async function openProfile(id: string) {
    if (expanded === id) { setExpanded(null); return }
    await reloadRules(id)
    setExpanded(id)
  }
  async function toggleProfile(profile: RoutingProfileRecord) {
    const result = await mutateCatalog<RoutingProfileRecord>(`/api/v1/routing-profiles/${profile.id}`, 'PATCH', { enabled: !profile.enabled }, csrfToken())
    if (result.kind === 'error') setError(result.message)
    else setGeneration((value) => value + 1)
  }
  async function toggleRule(profileID: string, rule: RuleRecord) {
    const result = await mutateCatalog<RuleRecord>(`/api/v1/routing-profiles/${profileID}/rules/${rule.id}`, 'PATCH', { enabled: !rule.enabled }, csrfToken())
    if (result.kind === 'error') setError(result.message)
    else await reloadRules(profileID)
  }
  async function removeRule(profileID: string, rule: RuleRecord) {
    if (!window.confirm(`删除分流规则「${rule.match_value}」？`)) return
    const result = await mutateCatalog<void>(`/api/v1/routing-profiles/${profileID}/rules/${rule.id}`, 'DELETE', null, csrfToken())
    if (result.kind === 'error') setError(result.message); else await reloadRules(profileID)
  }
  async function removeProfile(profile: RoutingProfileRecord) {
    if (!window.confirm(`删除分流 Profile「${profile.name}」及其规则？`)) return
    const result = await mutateCatalog<void>(`/api/v1/routing-profiles/${profile.id}`, 'DELETE', null, csrfToken())
    if (result.kind === 'error') setError(result.message); else { setExpanded(null); setGeneration((value) => value + 1) }
  }
  return <div className="catalog-page"><div className="catalog-title"><div><span className="section-overline">{section.eyebrow}</span><h1>{section.title}</h1><p>{section.description}</p></div><div className="catalog-title-actions"><button className="refresh-button" type="button" onClick={() => setGeneration((value) => value + 1)}><RefreshCw size={16} />刷新列表</button>{user.permissions.includes('routing.write') && <ProfileForm lines={lines} onSaved={() => setGeneration((value) => value + 1)} />}</div></div>
    {state === 'loading' && <div className="catalog-state" role="status">正在加载分流 Profile…</div>}{state === 'error' && <div className="catalog-state catalog-error">{error}</div>}{state === 'empty' && <div className="catalog-state"><ListFilter size={24} /><span>还没有分流 Profile。</span></div>}
    {state === 'ready' && <div className="routing-list">{profiles.map((profile) => <article className="catalog-card routing-card" key={profile.id}><div className="catalog-card-head"><span className="catalog-icon"><ListFilter size={18} /></span><span className={`status-chip ${profile.enabled ? 'status-online' : 'status-offline'}`}><i />{profile.enabled ? '启用' : '停用'}</span></div><div className="catalog-card-name"><strong>{profile.name}</strong><span>修订版本 {profile.revision} · fallback {profile.fallback_kind === 'direct' ? '直连' : profile.fallback_kind === 'block' ? '阻断' : '指定线路'}</span></div><div className="catalog-card-foot"><span>{rules[profile.id]?.length ?? '—'} 条规则</span>{user.permissions.includes('routing.write') && <><RuleForm profileID={profile.id} lines={lines} onSaved={() => { setExpanded(profile.id); void reloadRules(profile.id) }} /><ProfileForm editing={profile} lines={lines} onSaved={() => setGeneration((value) => value + 1)} /><button className="catalog-toggle" type="button" onClick={() => void toggleProfile(profile)}>{profile.enabled ? '停用 Profile' : '启用 Profile'}</button><button className="catalog-toggle" type="button" onClick={() => void removeProfile(profile)}><Trash2 size={14} />删除 Profile</button></>}<button className="catalog-text-button" type="button" onClick={() => void openProfile(profile.id)}>{expanded === profile.id ? '收起规则' : '查看规则'}</button></div>{expanded === profile.id && <div className="routing-rules">{(rules[profile.id] ?? []).map((rule) => <div className="routing-rule" key={rule.id}><span>{rule.priority}</span><strong>{rule.match_type}</strong><code>{rule.match_value}</code><em>{rule.action === 'line' ? '线路' : rule.action === 'direct' ? '直连' : '阻断'}</em>{user.permissions.includes('routing.write') && <><RuleForm profileID={profile.id} lines={lines} editing={rule} onSaved={() => void reloadRules(profile.id)} /><button className="catalog-text-button" type="button" onClick={() => void toggleRule(profile.id, rule)}>{rule.enabled ? '停用' : '启用'}</button><button className="catalog-text-button" type="button" onClick={() => void removeRule(profile.id, rule)}><Trash2 size={14} />删除</button></>}</div>)}{rules[profile.id]?.length === 0 && <span className="catalog-form-note">暂无规则，fallback 将处理未命中流量。</span>}</div>}</article>)}</div>}
    {error && state === 'ready' && <p className="catalog-page-error" role="alert">{error}</p>}
  </div>
}
