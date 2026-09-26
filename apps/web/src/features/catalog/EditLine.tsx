import { useState, type FormEvent } from 'react'
import { ArrowRight, Pencil } from 'lucide-react'
import { mutateCatalog, type LineRecord } from '../../lib/catalog'
import type { User } from '../../lib/dashboard'
import { csrfToken } from './CreateLine'
import { lineEditPath, lineEditPayload } from './lineAccess'

export function EditLine({ line, user, onSaved }: { line: LineRecord; user: User; onSaved: () => void }) {
  const path = lineEditPath(line, user)
  const shared = line.owner_user_id === null
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(line.name)
  const [priority, setPriority] = useState(line.priority)
  const [weight, setWeight] = useState(line.weight)
  const [tags, setTags] = useState(line.tags.join(', '))
  const [multiplier, setMultiplier] = useState(line.multiplier_milli === null ? '' : String(line.multiplier_milli / 1000))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  if (!path) return null

  function openEditor() {
    setName(line.name)
    setPriority(line.priority)
    setWeight(line.weight)
    setTags(line.tags.join(', '))
    setMultiplier(line.multiplier_milli === null ? '' : String(line.multiplier_milli / 1000))
    setError('')
    setOpen(true)
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!path) return
    const milli = multiplier.trim() === '' ? null : Math.round(Number(multiplier) * 1000)
    const payload = lineEditPayload({ name, priority, weight, tags, multiplier_milli: milli }, shared)
    if (new TextEncoder().encode(payload.name).length > 100) { setError('线路名称不能超过 100 字节。'); return }
    if (payload.tags.length > 16 || payload.tags.some((tag) => new TextEncoder().encode(tag).length > 32)) { setError('最多 16 个标签，每个标签不超过 32 字节。'); return }
    if (shared && milli !== null && (!Number.isInteger(milli) || milli < 1 || milli > 100000)) { setError('倍率须介于 0.001 和 100 之间。'); return }
    setBusy(true)
    setError('')
    const result = await mutateCatalog<LineRecord>(path, 'PATCH', payload, csrfToken())
    setBusy(false)
    if (result.kind === 'error') { setError(result.message); return }
    setOpen(false)
    onSaved()
  }

  return <>
    <button className="catalog-toggle" type="button" onClick={openEditor}><Pencil size={13} />编辑线路</button>
    {open && <div className="catalog-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (!busy && event.target === event.currentTarget) setOpen(false) }}>
      <form className="catalog-dialog" onSubmit={(event) => void submit(event)} aria-label={`编辑线路 ${line.name}`}>
        <div className="catalog-dialog-head"><span className="section-overline">EDIT LINE</span><h2>编辑线路</h2><p>{shared ? '共享线路，可调整名称、排序、标签和计费倍率。' : '调整自有线路的名称、排序和标签。节点拓扑保持不变。'}</p></div>
        <label htmlFor={`edit-line-name-${line.id}`}>线路名称</label><input id={`edit-line-name-${line.id}`} required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} />
        <div className="catalog-form-row"><div><label htmlFor={`edit-line-priority-${line.id}`}>优先级</label><input id={`edit-line-priority-${line.id}`} type="number" required min={0} max={1000} value={priority} onChange={(event) => setPriority(Number(event.target.value))} /></div><div><label htmlFor={`edit-line-weight-${line.id}`}>权重</label><input id={`edit-line-weight-${line.id}`} type="number" required min={1} max={100} value={weight} onChange={(event) => setWeight(Number(event.target.value))} /></div></div>
        <label htmlFor={`edit-line-tags-${line.id}`}>标签</label><input id={`edit-line-tags-${line.id}`} value={tags} onChange={(event) => setTags(event.target.value)} placeholder="多个标签用英文逗号分隔" /><small className="catalog-form-note">最多 16 个标签，每个标签不超过 32 字节。</small>
        {shared && <><label htmlFor={`edit-line-multiplier-${line.id}`}>计费倍率</label><input id={`edit-line-multiplier-${line.id}`} type="number" min={0.001} max={100} step={0.001} value={multiplier} onChange={(event) => setMultiplier(event.target.value)} placeholder="留空保持当前配置" /><small className="catalog-form-note">留空不会修改原有倍率。</small></>}
        {error && <div className="auth-error" role="alert">{error}</div>}
        <div className="catalog-form-actions"><button className="refresh-button" type="button" disabled={busy} onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={busy}>{busy ? '正在保存…' : '保存修改'}<ArrowRight size={16} /></button></div>
      </form>
    </div>}
  </>
}
