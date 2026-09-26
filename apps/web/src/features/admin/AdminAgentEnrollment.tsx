import { useRef, useState, type FormEvent } from 'react'
import { KeyRound } from 'lucide-react'
import { csrfToken } from '../catalog/CreateLine'
import { issueAgentEnrollmentToken, type AgentEnrollmentToken } from './agentEnrollment'

export function AdminAgentEnrollment({ nodeID, nodeName }: { nodeID: string; nodeName: string }) {
  const [open, setOpen] = useState(false)
  const [password, setPassword] = useState('')
  const [issued, setIssued] = useState<AgentEnrollmentToken | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const requestVersion = useRef(0)

  function close() {
    requestVersion.current += 1
    setOpen(false)
    setPassword('')
    setIssued(null)
    setError('')
    setCopied(false)
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError('')
    const version = ++requestVersion.current
    const result = await issueAgentEnrollmentToken(nodeID, password, csrfToken())
    if (version !== requestVersion.current) return
    setPassword('')
    setBusy(false)
    if (result.kind === 'error') setError(result.message)
    else setIssued(result.data)
  }

  async function copy() {
    if (!issued) return
    try {
      await navigator.clipboard.writeText(issued.token)
      setCopied(true)
    } catch {
      setError('复制失败，请手动复制令牌。')
    }
  }

  return <>
    <button className="refresh-button" type="button" onClick={() => setOpen(true)}><KeyRound size={13} />签发入网令牌</button>
    {open && <div className="catalog-dialog-backdrop" role="presentation">
      <form className="catalog-dialog" onSubmit={(event) => void submit(event)}>
        <div className="catalog-dialog-head">
          <span className="section-overline">AGENT ENROLLMENT</span>
          <h2>{nodeName} · Agent 入网</h2>
          <p>请输入当前账户密码。新令牌仅显示一次，有效期 10 分钟；再次签发会使旧令牌失效。</p>
        </div>
        {!issued ? <>
          <label htmlFor={`agent-password-${nodeID}`}>当前账户密码</label>
          <input id={`agent-password-${nodeID}`} type="password" autoComplete="current-password" required value={password} onChange={(event) => setPassword(event.target.value)} />
        </> : <>
          <label htmlFor={`agent-token-${nodeID}`}>一次性入网令牌</label>
          <textarea id={`agent-token-${nodeID}`} readOnly rows={4} value={issued.token} />
          <small className="catalog-form-note">过期时间：{new Date(issued.expires_at).toLocaleString('zh-CN')}。关闭后无法再次查看，请立即在目标服务器运行 Agent 入网命令，并通过标准输入传入令牌。</small>
        </>}
        {error && <div className="auth-error" role="alert">{error}</div>}
        <div className="catalog-form-actions">
          <button className="refresh-button" type="button" onClick={close} disabled={busy}>{issued ? '关闭并清除' : '取消'}</button>
          {issued ? <button className="primary-button" type="button" onClick={() => void copy()}>{copied ? '已复制' : '复制令牌'}</button> : <button className="primary-button" type="submit" disabled={busy}>{busy ? '签发中…' : '签发令牌'}</button>}
        </div>
      </form>
    </div>}
  </>
}
