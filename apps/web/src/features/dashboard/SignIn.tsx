import { useState, type FormEvent } from 'react'
import { LockKeyhole, ArrowRight } from 'lucide-react'

export function SignIn({ onSignedIn }: { onSignedIn: () => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      const response = await fetch('/api/v1/auth/login', {
        method: 'POST', credentials: 'same-origin', cache: 'no-store',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password }),
      })
      if (!response.ok) {
        setError(response.status === 401 ? '邮箱或密码不正确。' : response.status === 429 ? '尝试过于频繁，请稍后再试。' : response.status === 404 ? '此入口尚未开放登录。' : '登录暂不可用，请稍后重试。')
        return
      }
      setPassword('')
      onSignedIn()
    } catch {
      setError('无法连接账户服务，请稍后重试。')
    } finally {
      setBusy(false)
    }
  }

  return <section className="auth-page" aria-labelledby="sign-in-title">
    <div className="auth-intro"><span className="section-overline">SECURE WORKSPACE</span><h1 id="sign-in-title">登录工作台</h1><p>通过安全会话查看个人套餐、计费周期与真实流量。账户数据只在已登录后从服务端读取。</p></div>
    <form className="auth-card" onSubmit={submit}>
      <span className="auth-icon"><LockKeyhole size={24} /></span><h2>欢迎回来</h2><p>请输入账户邮箱和密码</p>
      <label htmlFor="login-email">邮箱</label><input id="login-email" type="email" autoComplete="username" required value={email} onChange={(event) => setEmail(event.target.value)} />
      <label htmlFor="login-password">密码</label><input id="login-password" type="password" autoComplete="current-password" required value={password} onChange={(event) => setPassword(event.target.value)} />
      {error && <div className="auth-error" role="alert">{error}</div>}
      <button className="primary-button" disabled={busy} type="submit">{busy ? '正在登录…' : '安全登录'}<ArrowRight size={17} /></button>
    </form>
  </section>
}
