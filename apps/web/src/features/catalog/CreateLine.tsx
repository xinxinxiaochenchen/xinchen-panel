import { useEffect, useState, type FormEvent } from 'react'
import { ArrowRight, Plus } from 'lucide-react'
import { loadCatalogPage, mutateCatalog, type LineRecord, type NodeRecord } from '../../lib/catalog'
import type { User } from '../../lib/dashboard'
import { draftLinePayload } from './lineDraft'
import { LineHopFields } from './LineHopFields'

function csrfToken(): string {
  return document.cookie.split('; ').find((value) => value.startsWith('__Host-control_csrf='))?.split('=')[1] ?? ''
}

export function CreateLine({ user, onSaved }: { user: User; onSaved: () => void }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [nodeID, setNodeID] = useState('')
  const [multiHop, setMultiHop] = useState(false)
  const [hopIDs, setHopIDs] = useState(['', ''])
  const [priority, setPriority] = useState(100)
  const [weight, setWeight] = useState(1)
  const [nodes, setNodes] = useState<NodeRecord[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [loadingNodes, setLoadingNodes] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const admin = user.permissions.includes('lines.write')
  const nodePath = user.permissions.includes('nodes.write') ? '/api/v1/admin/nodes' : '/api/v1/nodes'

  useEffect(() => {
    if (!open) return
    let active = true
    setNodes([])
    setCursor(null)
    setError('')
    setLoadingNodes(true)
    void loadCatalogPage<NodeRecord>(nodePath, null).then((result) => {
      if (!active) return
      if (result.kind === 'ready') { setNodes(result.data.items); setCursor(result.data.next_cursor) }
      else if (result.kind === 'error') setError(result.message)
      setLoadingNodes(false)
    })
    return () => { active = false }
  }, [open, nodePath])

  async function loadMoreNodes() {
    if (!cursor || loadingNodes) return
    setLoadingNodes(true)
    const result = await loadCatalogPage<NodeRecord>(nodePath, cursor)
    if (result.kind === 'ready') { setNodes((current) => [...current, ...result.data.items]); setCursor(result.data.next_cursor) }
    else if (result.kind === 'empty') setCursor(null)
    else if (result.kind === 'error') setError(result.message)
    setLoadingNodes(false)
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!multiHop && !nodeID) { setError('请选择代理出口节点。'); return }
    let payload: unknown
    try {
      payload = multiHop ? draftLinePayload(name, hopIDs, priority, weight) : { name: name.trim(), node_id: nodeID, priority, weight }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '线路拓扑无效。')
      return
    }
    setBusy(true)
    setError('')
    const path = admin ? '/api/v1/admin/lines' : '/api/v1/lines'
    const result = await mutateCatalog<LineRecord>(path, 'POST', payload, csrfToken())
    setBusy(false)
    if (result.kind === 'error') { setError(result.message); return }
    setOpen(false)
    setName('')
    setNodeID('')
    setHopIDs(['', ''])
    onSaved()
  }

  return <>
    <button className="primary-button catalog-create-button" type="button" onClick={() => setOpen(true)}><Plus size={16} />创建线路</button>
    {open && <div className="catalog-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setOpen(false) }}>
      <form className="catalog-dialog" onSubmit={(event) => void submit(event)} aria-label="创建线路">
        <div className="catalog-dialog-head"><span className="section-overline">NEW LINE</span><h2>{multiHop ? '创建多跳线路' : '创建单跳线路'}</h2><p>选择套餐允许的节点，组成自己的线路。</p></div>
        <label htmlFor="line-mode">线路类型</label><select id="line-mode" value={multiHop ? 'draft' : 'single'} onChange={(event) => setMultiHop(event.target.value === 'draft')}><option value="single">单跳线路</option><option value="draft">多跳线路（创建后启用）</option></select>
        <label htmlFor="line-name">线路名称</label><input id="line-name" required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} placeholder="例如 日本日常线路" />
        {multiHop ? <LineHopFields ids={hopIDs} nodes={nodes} onChange={setHopIDs} /> : <><label htmlFor="line-node">出口节点</label><select id="line-node" required value={nodeID} onChange={(event) => setNodeID(event.target.value)}><option value="">请选择节点</option>{nodes.filter((node) => node.enabled && node.capabilities.includes('proxy')).map((node) => <option key={node.id} value={node.id}>{node.name} · {node.group_code} · {node.region}</option>)}</select></>}
        {loadingNodes && <small className="catalog-form-note">正在加载可用节点…</small>}
        {cursor && <button className="catalog-text-button" type="button" disabled={loadingNodes} onClick={() => void loadMoreNodes()}>加载更多节点</button>}
        <div className="catalog-form-row"><div><label htmlFor="line-priority">优先级</label><input id="line-priority" type="number" min={0} max={1000} value={priority} onChange={(event) => setPriority(Number(event.target.value))} /></div><div><label htmlFor="line-weight">权重</label><input id="line-weight" type="number" min={1} max={100} value={weight} onChange={(event) => setWeight(Number(event.target.value))} /></div></div>
        {error && <div className="auth-error" role="alert">{error}</div>}
        <div className="catalog-form-actions"><button type="button" className="refresh-button" onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={busy || loadingNodes}>{busy ? '正在创建…' : multiHop ? '保存停用草稿' : '创建线路'}<ArrowRight size={16} /></button></div>
      </form>
    </div>}
  </>
}

export { csrfToken }
