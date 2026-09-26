import { useState, type FormEvent } from 'react'
import { Plus, ShieldCheck } from 'lucide-react'
import { mutateCatalog } from '../../lib/catalog'
import { buildForwardPolicyInput, type ForwardPolicyDraft, type ForwardTargetPolicy, type ResourceGroup } from '../../lib/admin'
import { csrfToken } from '../catalog/CreateLine'
import { AdminSection } from './AdminSection'

export function AdminForwardPolicies({ policies, groups, onRefresh }: { policies: ForwardTargetPolicy[]; groups: ResourceGroup[]; onRefresh: () => void }) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<ForwardPolicyDraft>({ kind: 'public_host', groupID: '', protocol: 'TCP', portStart: 443, portEnd: 443 })
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')
    let body: ReturnType<typeof buildForwardPolicyInput>
    try { body = buildForwardPolicyInput(draft) }
    catch (caught) { setError(caught instanceof Error ? caught.message : '目标策略无效。'); return }
    setBusy('create')
    const result = await mutateCatalog<ForwardTargetPolicy>('/api/v1/admin/forward-target-policies', 'POST', body, csrfToken())
    setBusy('')
    if (result.kind === 'error') { setError(result.message); return }
    setOpen(false)
    setDraft({ kind: 'public_host', groupID: '', protocol: 'TCP', portStart: 443, portEnd: 443 })
    onRefresh()
  }

  async function toggle(policy: ForwardTargetPolicy) {
    setError('')
    setBusy(policy.id)
    const result = await mutateCatalog<ForwardTargetPolicy>(`/api/v1/admin/forward-target-policies/${policy.id}`, 'PATCH', { enabled: !policy.enabled }, csrfToken())
    setBusy('')
    if (result.kind === 'error') setError(result.message)
    else onRefresh()
  }

  const groupName = (id: string | null) => groups.find((group) => group.id === id)?.code ?? id ?? '未分组'
  return <AdminSection title="转发目标策略" description="目标默认禁止访问。管理员按目标类型、协议和端口范围开放；停用策略会触发相关规则重新收敛。" action={<button className="primary-button catalog-create-button" type="button" onClick={() => setOpen(true)}><ShieldCheck size={16} />新增策略</button>}>
    <div className="admin-table">
      {policies.map((policy) => <div className="admin-row admin-policy-row" key={policy.id}><strong>{policy.kind === 'node' ? `节点 · ${groupName(policy.target_group_id)}` : '公网地址'}</strong><span>{policy.protocol} · {policy.port_start === policy.port_end ? policy.port_start : `${policy.port_start}–${policy.port_end}`}</span><em>{policy.enabled ? '启用' : '停用'}</em><button className="refresh-button" type="button" disabled={busy !== ''} onClick={() => void toggle(policy)}>{busy === policy.id ? '处理中…' : policy.enabled ? '停用' : '启用'}</button></div>)}
      {policies.length === 0 && <div className="catalog-form-note">暂无目标策略，所有转发目标默认禁止访问。</div>}
    </div>
    {error && !open && <p className="catalog-page-error" role="alert">{error}</p>}
    {open && <div className="catalog-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setOpen(false) }}><form className="catalog-dialog" onSubmit={(event) => void submit(event)} aria-label="创建转发目标策略">
      <div className="catalog-dialog-head"><span className="section-overline">FORWARD TARGET POLICY</span><h2>开放目标范围</h2><p>公网策略覆盖所有公网地址，请尽量收窄协议和端口范围。内网地址仍受服务端拦截。</p></div>
      <label htmlFor="policy-kind">目标类型</label><select id="policy-kind" value={draft.kind} onChange={(event) => setDraft((current) => ({ ...current, kind: event.target.value as ForwardPolicyDraft['kind'], groupID: '' }))}><option value="public_host">公网地址</option><option value="node" disabled={!groups.some((group) => group.enabled)}>指定资源域内的节点</option></select>
      {draft.kind === 'node' && <><label htmlFor="policy-group">目标资源域</label><select id="policy-group" required value={draft.groupID} onChange={(event) => setDraft((current) => ({ ...current, groupID: event.target.value }))}><option value="">请选择资源域</option>{groups.filter((group) => group.enabled).map((group) => <option key={group.id} value={group.id}>{group.code} · {group.name}</option>)}</select></>}
      <label htmlFor="policy-protocol">协议</label><select id="policy-protocol" value={draft.protocol} onChange={(event) => setDraft((current) => ({ ...current, protocol: event.target.value as ForwardPolicyDraft['protocol'] }))}><option value="TCP">TCP</option><option value="UDP">UDP</option></select>
      <div className="catalog-form-row"><div><label htmlFor="policy-port-start">起始端口</label><input id="policy-port-start" type="number" min={1} max={65535} required value={draft.portStart} onChange={(event) => setDraft((current) => ({ ...current, portStart: Number(event.target.value) }))} /></div><div><label htmlFor="policy-port-end">结束端口</label><input id="policy-port-end" type="number" min={1} max={65535} required value={draft.portEnd} onChange={(event) => setDraft((current) => ({ ...current, portEnd: Number(event.target.value) }))} /></div></div>
      {error && <div className="auth-error" role="alert">{error}</div>}
      <div className="catalog-form-actions"><button className="refresh-button" type="button" onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={busy !== ''}>{busy === 'create' ? '创建中…' : '创建策略'}<Plus size={16} /></button></div>
    </form></div>}
  </AdminSection>
}
