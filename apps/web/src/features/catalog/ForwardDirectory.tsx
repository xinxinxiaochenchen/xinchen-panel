import { useEffect, useState, type FormEvent } from 'react'
import { ArrowRight, GitBranch, RefreshCw } from 'lucide-react'
import type { Section } from '../../app/sections'
import { loadAllCatalogPages, loadCatalogPage, mutateCatalog, type ForwardRecord, type LineRecord, type NodeRecord } from '../../lib/catalog'
import type { User } from '../../lib/dashboard'
import { csrfToken } from './CreateLine'

const statusLabel: Record<string, string> = { pending: '等待下发', active: '运行中', apply_failed: '下发失败', disabled: '已停用' }

function ForwardForm({ user, onSaved }: { user: User; onSaved: () => void }) {
  const [open, setOpen] = useState(false)
  const [nodes, setNodes] = useState<NodeRecord[]>([])
  const [lines, setLines] = useState<LineRecord[]>([])
  const [name, setName] = useState('')
  const [ingress, setIngress] = useState('')
  const [targetNode, setTargetNode] = useState('')
  const [targetHost, setTargetHost] = useState('')
  const [lineID, setLineID] = useState('')
  const [ingressPort, setIngressPort] = useState(10000)
  const [targetPort, setTargetPort] = useState(443)
  const [protocol, setProtocol] = useState<'TCP' | 'UDP' | 'BOTH'>('TCP')
  const [mode, setMode] = useState<'node' | 'host' | 'line'>('node')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const nodePath = user.permissions.includes('nodes.write') ? '/api/v1/admin/nodes' : '/api/v1/nodes'

  useEffect(() => {
    if (!open) return
    let active = true
    void loadAllCatalogPages<NodeRecord>(nodePath).then((result) => {
      if (!active) return
      if (result.kind === 'ready') setNodes(result.data)
      else if (result.kind === 'error') setError(result.message)
    })
    void loadAllCatalogPages<LineRecord>('/api/v1/lines').then((result) => {
      if (active && result.kind === 'ready') setLines(result.data)
    })
    return () => { active = false }
  }, [open, nodePath])

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true); setError('')
    const body: Record<string, unknown> = { name: name.trim(), ingress_node_id: ingress, ingress_port: ingressPort, target_port: targetPort, protocol }
    if (mode === 'node') body.target_node_id = targetNode
    else body.target_host = targetHost.trim()
    if (mode === 'line') body.line_id = lineID
    const result = await mutateCatalog<ForwardRecord>('/api/v1/forward-rules', 'POST', body, csrfToken())
    setBusy(false)
    if (result.kind === 'error') { setError(result.message); return }
    setOpen(false); setName(''); setIngress(''); setTargetNode(''); setTargetHost(''); setLineID(''); onSaved()
  }

  return <>
    <button className="primary-button catalog-create-button" type="button" onClick={() => setOpen(true)}><GitBranch size={16} />创建转发</button>
    {open && <div className="catalog-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setOpen(false) }}><form className="catalog-dialog" onSubmit={(event) => void submit(event)} aria-label="创建转发规则">
      <div className="catalog-dialog-head"><span className="section-overline">NEW FORWARD</span><h2>创建转发规则</h2><p>入口节点、目标和协议会受套餐额度及管理员策略约束。</p></div>
      <label htmlFor="forward-name">规则名称</label><input id="forward-name" required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} placeholder="例如 Web 服务" />
      <label htmlFor="forward-ingress">入口节点</label><select id="forward-ingress" required value={ingress} disabled={mode === 'line'} onChange={(event) => setIngress(event.target.value)}><option value="">{mode === 'line' ? '由线路自动确定入口' : '请选择中转入口'}</option>{nodes.filter((node) => node.enabled && node.capabilities.includes('forward')).map((node) => <option key={node.id} value={node.id}>{node.name} · {node.group_code}</option>)}</select>
      <div className="catalog-form-row"><div><label htmlFor="forward-ingress-port">入口端口</label><input id="forward-ingress-port" type="number" min={1024} max={65535} value={ingressPort} onChange={(event) => setIngressPort(Number(event.target.value))} /></div><div><label htmlFor="forward-target-port">目标端口</label><input id="forward-target-port" type="number" min={1} max={65535} value={targetPort} onChange={(event) => setTargetPort(Number(event.target.value))} /></div></div>
      <label htmlFor="forward-protocol">协议</label><select id="forward-protocol" value={protocol} onChange={(event) => setProtocol(event.target.value as typeof protocol)}><option value="TCP">TCP</option><option value="UDP">UDP</option><option value="BOTH">TCP + UDP</option></select>
      <div className="catalog-segmented"><button type="button" className={mode === 'node' ? 'active' : ''} onClick={() => setMode('node')}>授权节点</button><button type="button" className={mode === 'host' ? 'active' : ''} onClick={() => setMode('host')}>公网地址</button><button type="button" className={mode === 'line' ? 'active' : ''} onClick={() => { setMode('line'); setIngress(lines.find((line) => line.id === lineID)?.hops[0]?.node_id ?? '') }}>经线路转发</button></div>
      {mode === 'line' && <><label htmlFor="forward-line">转发线路</label><select id="forward-line" required value={lineID} onChange={(event) => { const selected = event.target.value; setLineID(selected); const line = lines.find((item) => item.id === selected); if (line) setIngress(line.hops[0]?.node_id ?? '') }}><option value="">请选择已启用的多跳线路</option>{lines.filter((line) => line.enabled && line.hops.length > 1).map((line) => <option key={line.id} value={line.id}>{line.name} · {line.hops.length} 跳</option>)}</select></>}
      {mode === 'node' ? <><label htmlFor="forward-target-node">目标节点</label><select id="forward-target-node" required value={targetNode} onChange={(event) => setTargetNode(event.target.value)}><option value="">请选择目标节点</option>{nodes.filter((node) => node.enabled).map((node) => <option key={node.id} value={node.id}>{node.name} · {node.group_code}</option>)}</select></> : <><label htmlFor="forward-target-host">目标公网地址</label><input id="forward-target-host" required value={targetHost} onChange={(event) => setTargetHost(event.target.value)} placeholder="例如 service.example.com" /></>}
      {error && <div className="auth-error" role="alert">{error}</div>}
      <div className="catalog-form-actions"><button type="button" className="refresh-button" onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={busy}>{busy ? '正在创建…' : '创建转发'}<ArrowRight size={16} /></button></div>
    </form></div>}
  </>
}

export function ForwardDirectory({ section, user }: { section: Section; user: User }) {
  const [generation, setGeneration] = useState(0)
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'error'>('loading')
  const [items, setItems] = useState<ForwardRecord[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [loadingMore, setLoadingMore] = useState(false)
  async function load(first = true) {
    if (first) { setState('loading'); setItems([]); setCursor(null) }
    const result = await loadCatalogPage<ForwardRecord>('/api/v1/forward-rules', first ? null : cursor)
    if (result.kind === 'ready') { setState('ready'); setItems((current) => first ? result.data.items : [...current, ...result.data.items]); setCursor(result.data.next_cursor) }
    else if (result.kind === 'empty') { if (first) setState('empty'); setCursor(null) }
    else if (result.kind === 'error') { if (first) setState('error'); setError(result.message) }
  }
  useEffect(() => { void load() }, [generation])
  async function toggle(rule: ForwardRecord) {
    const result = await mutateCatalog<ForwardRecord>(`/api/v1/forward-rules/${rule.id}`, 'PATCH', { enabled: !rule.enabled }, csrfToken())
    if (result.kind === 'error') setError(result.message); else setGeneration((value) => value + 1)
  }
  return <div className="catalog-page"><div className="catalog-title"><div><span className="section-overline">{section.eyebrow}</span><h1>{section.title}</h1><p>{section.description}</p></div><div className="catalog-title-actions"><button className="refresh-button" type="button" onClick={() => setGeneration((value) => value + 1)}><RefreshCw size={16} />刷新列表</button>{user.permissions.includes('forward_rules.write') && <ForwardForm user={user} onSaved={() => setGeneration((value) => value + 1)} />}</div></div>
    {state === 'loading' && <div className="catalog-state" role="status">正在加载转发规则…</div>}
    {state === 'error' && <div className="catalog-state catalog-error">{error}</div>}
    {state === 'empty' && <div className="catalog-state"><GitBranch size={24} /><span>还没有转发规则。</span></div>}
    {state === 'ready' && <div className="forward-list">{items.map((rule) => <article className="catalog-card forward-card" key={rule.id}><div className="catalog-card-head"><span className="catalog-icon"><GitBranch size={18} /></span><span className={`status-chip status-${rule.enabled ? 'online' : 'offline'}`}><i />{rule.enabled ? statusLabel[rule.apply_status] ?? '已启用' : '已停用'}</span></div><div className="catalog-card-name"><strong>{rule.name}</strong><span>{rule.protocol} · 入口端口 {rule.ingress_port}{rule.line_id ? ' · 多跳线路' : ''}</span></div><div className="forward-path"><span>{rule.ingress_node_id.slice(0, 8)}:{rule.ingress_port}</span><ArrowRight size={15} /><span>{rule.target_node_id ? rule.target_node_id.slice(0, 8) : rule.target_host}:{rule.target_port}</span></div><div className="catalog-card-foot"><span>应用状态：{statusLabel[rule.apply_status] ?? rule.apply_status}</span>{user.permissions.includes('forward_rules.write') && <button className="catalog-toggle" type="button" onClick={() => void toggle(rule)}>{rule.enabled ? '停用规则' : '启用规则'}</button>}</div></article>)}</div>}
    {cursor && <button className="load-more-button" type="button" disabled={loadingMore} onClick={async () => { setLoadingMore(true); await load(false); setLoadingMore(false) }}>{loadingMore ? '正在加载…' : '加载更多'}</button>}
    {error && state === 'ready' && <p className="catalog-page-error" role="alert">{error}</p>}
  </div>
}
