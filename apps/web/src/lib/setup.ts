export type InitialSetupInput = { token: string; email: string; password: string; confirmation: string }

export function validateInitialSetup(input: InitialSetupInput): string {
  if (!/^[A-Za-z0-9_-]{43}$/.test(input.token.trim())) return '请填写安装完成时显示的初始化凭证。'
  if (!input.email.trim()) return '请填写管理员邮箱。'
  const bytes = new TextEncoder().encode(input.password).length
  if (bytes < 12 || bytes > 72) return '密码需要 12–72 字节，中文字符通常占 3 字节。'
  if (input.password !== input.confirmation) return '两次输入的密码不一致。'
  return ''
}

export async function initializePanel(input: InitialSetupInput, request: typeof fetch = fetch): Promise<void> {
  const invalid = validateInitialSetup(input)
  if (invalid) throw new Error(invalid)
  const response = await request('/api/v1/setup', {
    method: 'POST', credentials: 'same-origin', cache: 'no-store',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token: input.token.trim(), email: input.email.trim(), password: input.password }),
  })
  if (response.status === 201) return
  if (response.status === 403) throw new Error('初始化凭证不正确，请核对安装完成时显示的凭证。')
  if (response.status === 400) throw new Error('请使用有效邮箱及 12–72 字节的密码。')
  if (response.status === 409) {
    const body = await response.json().catch(() => null) as { error?: { code?: string } } | null
    throw new Error(body?.error?.code === 'SETUP_COMPLETE' ? '初始化已完成，请刷新后登录。' : '该邮箱已被使用，请填写其他邮箱。')
  }
  if (response.status === 429) throw new Error('尝试过于频繁，请稍后再试。')
  if (response.status === 503) throw new Error('初始化入口暂不可用，请联系部署管理员。')
  throw new Error('初始化暂不可用，请稍后重试。')
}
