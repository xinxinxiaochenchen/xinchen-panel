import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Activity, ArrowUpRight, CircleDashed, Gauge, Globe2, Layers3, RefreshCw, Route, Server } from 'lucide-react'
import type { Section } from '../../app/sections'
import { capabilityLabel, loadCatalogPage, nodeStatusLabel, type CatalogPage, type CatalogResource, type LineRecord, type NodeRecord } from '../../lib/catalog'
import type { User } from '../../lib/dashboard'
import { CreateLine, csrfToken } from './CreateLine'
import { mutateCatalog } from '../../lib/catalog'
import { lineTogglePath } from './lineAccess'
import { EditLine } from './EditLine'

function DataState<T>({ state, empty }: { state: CatalogResource<T>; empty: string }) {
  if (state.kind === 'loading') return <div className="catalog-state" role="status">正在加载资源…</div>
  if (state.kind === 'empty') return <div className="catalog-state"><CircleDashed size={24} /><span>{empty}</span></div>
  if (state.kind === 'error') return <div className="catalog-state catalog-error"><span>{state.message}</span></div>
  return null
}

function PageTitle({ section, onRefresh, action }: { section: Section; onRefresh: () => void; action?: ReactNode }) {
  return <div className="catalog-title">
    <div>
      <span className="section-overline">{section.eyebrow}</span>
      <h1>{section.title}</h1>
      <p>{section.description}</p>
    </div>
    <div className="catalog-title-actions"><button className="refresh-button" type="button" onClick={onRefresh}><RefreshCw size={16} />刷新列表</button>{action}</div>
  </div>
}

function NodeCard({ node }: { node: NodeRecord }) {
  const address = node.public_ip || node.host
  return <article className="catalog-card">
    <div className="catalog-card-head"><span className="catalog-icon"><Server size={18} /></span><span className={`status-chip status-${node.agent_status}`}><i />{nodeStatusLabel(node.agent_status)}</span></div>
    <div className="catalog-card-name"><strong>{node.name}</strong><span>{node.group_code} · {node.region}</span></div>
    <div className="catalog-detail-grid"><div><small>地址</small><strong>{address}</strong></div><div><small>代理端口</small><strong>{node.proxy_port ?? '—'}</strong></div><div><small>倍率</small><strong>×{(node.multiplier_milli / 1000).toFixed(2)}</strong></div><div><small>能力</small><strong>{node.capabilities.map(capabilityLabel).join(' / ')}</strong></div></div>
    <div className="catalog-card-foot"><span>{node.tags.length ? node.tags.join(' · ') : '未设置标签'}</span>{node.last_seen_at && <span>最近心跳 {new Date(node.last_seen_at).toLocaleString('zh-CN')}</span>}</div>
  </article>
}

function LineCard({ line, user, onChanged }: { line: LineRecord; user: User; onChanged: () => void }) {
  const multiplier = line.multiplier_milli == null ? '套餐倍率' : `×${(line.multiplier_milli / 1000).toFixed(2)}`
  const togglePath = lineTogglePath(line, user)
  const canToggle = togglePath !== null
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function toggle() {
    setBusy(true)
    setError('')
    const result = await mutateCatalog<LineRecord>(togglePath!, 'PATCH', { enabled: !line.enabled }, csrfToken())
    setBusy(false)
    if (result.kind === 'error') setError(result.message)
    else onChanged()
  }
  return <article className="catalog-card">
    <div className="catalog-card-head"><span className="catalog-icon"><Route size={18} /></span><span className={`status-chip ${line.enabled ? 'status-online' : 'status-offline'}`}><i />{line.enabled ? '启用' : '停用'}</span></div>
    <div className="catalog-card-name"><strong>{line.name}</strong><span>{line.owner_user_id ? '我的线路' : '共享线路'}</span></div>
    <div className="line-topology">{line.hops.length ? line.hops.map((hop, index) => <span className="hop" key={`${hop.position}-${hop.node_id}`}><span>{({ ingress: '入口', relay: '中转', egress: '出口' } as Record<string, string>)[hop.role] ?? hop.role}</span><strong>{hop.node_id.slice(0, 8)}</strong>{index < line.hops.length - 1 && <ArrowUpRight size={14} />}</span>) : <span className="muted">暂无拓扑信息</span>}</div>
    <div className="catalog-detail-grid"><div><small>优先级</small><strong>{line.priority}</strong></div><div><small>权重</small><strong>{line.weight}</strong></div><div><small>倍率</small><strong>{multiplier}</strong></div><div><small>标签</small><strong>{line.tags.length ? line.tags.join(' / ') : '—'}</strong></div></div>
    <div className="catalog-card-foot"><span>{line.hops.length === 1 ? '单跳线路' : `${line.hops.length} 跳拓扑草稿，暂不可用`}</span><span><Layers3 size={13} /> {line.hops.length} 个节点</span></div>
    {canToggle && <div className="catalog-card-actions"><EditLine line={line} user={user} onSaved={onChanged} />{line.hops.length === 1 && <button className="catalog-toggle" type="button" disabled={busy} onClick={() => void toggle()}>{busy ? '正在保存…' : line.enabled ? '停用线路' : '启用线路'}</button>}</div>}
    {error && <p className="catalog-page-error" role="alert">{error}</p>}
  </article>
}

export function CatalogDirectory({ section, user }: { section: Section; user: User }) {
  const isNodes = section.id === 'nodes'
  const [generation, setGeneration] = useState(0)
  const [pages, setPages] = useState<CatalogPage<NodeRecord | LineRecord>[]>([])
  const [state, setState] = useState<CatalogResource<NodeRecord | LineRecord>>({ kind: 'loading' })
  const [pageError, setPageError] = useState('')
  const [loadingMore, setLoadingMore] = useState(false)
  const [cursor, setCursor] = useState<string | null>(null)
  const path = isNodes ? user.permissions.includes('nodes.write') ? '/api/v1/admin/nodes' : '/api/v1/nodes' : user.permissions.includes('lines.write') ? '/api/v1/admin/lines' : '/api/v1/lines'

  useEffect(() => {
    let active = true
    setPages([])
    setCursor(null)
    setState({ kind: 'loading' })
    setPageError('')
    void loadCatalogPage<NodeRecord | LineRecord>(path, null).then((result) => {
      if (!active) return
      setState(result)
      if (result.kind === 'ready') { setPages([result.data]); setCursor(result.data.next_cursor) }
    })
    return () => { active = false }
  }, [path, generation])

  const items = useMemo(() => pages.flatMap((page) => page.items), [pages])
  async function loadMore() {
    if (!cursor || loadingMore) return
    setLoadingMore(true)
    setPageError('')
    const result = await loadCatalogPage<NodeRecord | LineRecord>(path, cursor)
    if (result.kind === 'ready') { setPages((current) => [...current, result.data]); setCursor(result.data.next_cursor) }
    else if (result.kind === 'empty') setCursor(null)
    else if (result.kind === 'error') setPageError(result.message)
    setLoadingMore(false)
  }

  return <div className="catalog-page">
    <PageTitle section={section} onRefresh={() => setGeneration((value) => value + 1)} action={!isNodes && (user.permissions.includes('lines.write.self') || user.permissions.includes('lines.write')) ? <CreateLine user={user} onSaved={() => setGeneration((value) => value + 1)} /> : undefined} />
    {state.kind !== 'ready' && <DataState state={state} empty={isNodes ? '套餐当前没有可用节点。' : '套餐当前没有可用线路。'} />}
    {items.length > 0 && <div className="catalog-summary"><span><Activity size={15} />实时授权目录</span><strong>{items.length}{cursor ? '+' : ''} 项</strong></div>}
    {items.length > 0 && <div className="catalog-grid">{isNodes ? items.map((item) => <NodeCard key={item.id} node={item as NodeRecord} />) : items.map((item) => <LineCard key={item.id} line={item as LineRecord} user={user} onChanged={() => setGeneration((value) => value + 1)} />)}</div>}
    {pageError && <p className="catalog-page-error" role="alert">{pageError}</p>}
    {cursor && <button className="load-more-button" type="button" disabled={loadingMore} onClick={() => void loadMore()}><Gauge size={16} />{loadingMore ? '正在加载…' : '加载更多'}</button>}
    {state.kind === 'ready' && items.length === 0 && <div className="catalog-state"><Globe2 size={24} /><span>{isNodes ? '暂无节点数据。' : '暂无线路数据。'}</span></div>}
  </div>
}
