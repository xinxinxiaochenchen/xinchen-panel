import { useState, type FormEvent } from 'react'
import { ArrowRight, ShieldCheck } from 'lucide-react'
import { initializePanel, type InitialSetupInput } from '../../lib/setup'

export function InitialSetup({ enabled, onInitialized }: { enabled: boolean; onInitialized: () => void }) {
  const [input, setInput] = useState<InitialSetupInput>({ token: '', email: '', password: '', confirmation: '' })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const change = (key: keyof InitialSetupInput, value: string) => setInput((current) => ({ ...current, [key]: value }))

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!enabled || busy) return
    setBusy(true)
    setError('')
    try {
      await initializePanel(input)
      setInput({ token: '', email: '', password: '', confirmation: '' })
      window.location.hash = '#/admin'
      onInitialized()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法连接初始化服务，请稍后重试。')
    } finally {
      setBusy(false)
    }
  }

  return <section className="auth-page" aria-labelledby="setup-title">
    <div className="auth-intro"><span className="section-overline">GET STARTED</span><h1 id="setup-title">初始化面板</h1><p>创建管理员账号，登录后在管理页配置资源、节点、套餐额度、倍率和业务限制。</p><p>初始化凭证由安装器自动生成，用于确认服务器的管理权。管理员邮箱和密码在此设置。</p></div>
    <form className="auth-card" onSubmit={submit}>
      <span className="auth-icon"><ShieldCheck size={24} /></span><h2>创建管理员</h2><p>初始化仅需完成一次</p>
      {!enabled && <div className="auth-error" role="alert">初始化入口暂不可用，请联系部署管理员。</div>}
      <label htmlFor="setup-token">初始化凭证</label><input id="setup-token" type="password" autoComplete="off" required value={input.token} disabled={!enabled || busy} onChange={(event) => change('token', event.target.value)} />
      <label htmlFor="setup-email">管理员邮箱</label><input id="setup-email" type="email" autoComplete="username" maxLength={254} required value={input.email} disabled={!enabled || busy} onChange={(event) => change('email', event.target.value)} />
      <label htmlFor="setup-password">管理员密码</label><input id="setup-password" type="password" autoComplete="new-password" required value={input.password} disabled={!enabled || busy} aria-describedby="setup-password-help" onChange={(event) => change('password', event.target.value)} />
      <p id="setup-password-help">使用 12–72 字节的独立密码。</p>
      <label htmlFor="setup-confirmation">确认密码</label><input id="setup-confirmation" type="password" autoComplete="new-password" required value={input.confirmation} disabled={!enabled || busy} onChange={(event) => change('confirmation', event.target.value)} />
      {error && <div className="auth-error" role="alert">{error}</div>}
      <button className="primary-button" disabled={!enabled || busy} type="submit">{busy ? '正在初始化…' : '创建管理员并前往登录'}<ArrowRight size={17} /></button>
      <button className="refresh-button" type="button" disabled={busy} onClick={onInitialized}>已完成初始化？刷新状态</button>
    </form>
  </section>
}
