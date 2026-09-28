export type User = {
  id: string
  email: string
  status: string
  timezone: string
  roles: string[]
  permissions: string[]
}

export type PlanLimits = {
  max_forward_rules_per_node: number
  max_subscriptions: number
  max_routing_rules: number
  allow_custom_lines: boolean
  max_custom_lines: number
  max_hops: number
  max_proxy_lines: number
}
export type Snapshot = {
  plan_name: string
  billing_mode?: string
  period_months?: number
  quota_bytes: number
  default_multiplier_milli?: number
  limits?: Partial<PlanLimits>
  resource_group_ids?: string[]
  line_ids?: string[]
}
export type Membership = { id: string; status: string; starts_at: string; ends_at: string; anchor_day: number; timezone: string; snapshot: Snapshot }
export type ScopeNode = { id: string; group_id: string; group_code: string; name: string }
export type ScopeLine = { id: string; name: string }
export type PlanScopeLabels = { resourceGroups: string[]; lines: string[] }
export type Usage = {
  starts_at: string
  ends_at: string
  membership_ends_at: string
  quota_bytes: number
  uploaded_bytes: number
  downloaded_bytes: number
  charged_bytes: number
  reserved_bytes: number
  remaining_bytes: number
  available_bytes: number
  usage_percent: number
  snapshot: Snapshot
}
export type DailyUsage = { date: string; uploaded_bytes: number; downloaded_bytes: number; charged_bytes: number }
export type Viewer = { kind: 'preview' } | { kind: 'guest' } | { kind: 'signed-in'; user: User }
export type Resource<T> = { kind: 'loading' } | { kind: 'empty' } | { kind: 'ready'; data: T } | { kind: 'error'; message: string }

export function planScopeLabels(snapshot: Pick<Snapshot, 'resource_group_ids' | 'line_ids'>, nodes: ScopeNode[], lines: ScopeLine[]): PlanScopeLabels {
  const groups = new Map<string, string>()
  for (const node of nodes) {
    if (node.group_id && node.group_code && !groups.has(node.group_id)) groups.set(node.group_id, node.group_code)
  }
  const lineNames = new Map(lines.map((line) => [line.id, line.name]))
  return {
    resourceGroups: (snapshot.resource_group_ids ?? []).map((id) => groups.get(id)).filter((value): value is string => Boolean(value)),
    lines: (snapshot.line_ids ?? []).map((id) => lineNames.get(id)).filter((value): value is string => Boolean(value)),
  }
}

export function isSecureLocation(protocol: string, hostname: string): boolean {
  return protocol === 'https:' || protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]', '::1'].includes(hostname)
}

export async function loadViewer(protocol: string, hostname: string, request: typeof fetch = fetch): Promise<Viewer> {
  if (!isSecureLocation(protocol, hostname)) return { kind: 'preview' }
  const response = await request('/api/v1/me', { credentials: 'same-origin', cache: 'no-store' })
  if (response.status === 404) return { kind: 'preview' }
  if (response.status === 401) return { kind: 'guest' }
  if (!response.ok) throw new Error(`账户状态暂不可用（${response.status}）`)
  return { kind: 'signed-in', user: await response.json() as User }
}

export async function loadResource<T>(path: string, request: typeof fetch = fetch): Promise<Resource<T>> {
  try {
    const response = await request(path, { credentials: 'same-origin', cache: 'no-store' })
    if (response.status === 404) return { kind: 'empty' }
    if (response.status === 401) return { kind: 'error', message: '登录已过期，请重新登录。' }
    if (response.status === 403) return { kind: 'error', message: '当前账户无权查看此数据。' }
    if (!response.ok) throw new Error(`请求失败（${response.status}）`)
    return { kind: 'ready', data: await response.json() as T }
  } catch {
    return { kind: 'error', message: '数据暂不可用，请稍后重试。' }
  }
}

export function dailyRange(now: Date, days: number): { from: string; to: string } {
  const end = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()))
  const start = new Date(end)
  start.setUTCDate(start.getUTCDate() - Math.min(Math.max(days, 1), 90) + 1)
  return { from: start.toISOString().slice(0, 10), to: end.toISOString().slice(0, 10) }
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const unit = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / 1024 ** unit
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: value < 10 ? 2 : 1 }).format(value)} ${units[unit]}`
}

export function formatDate(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'short', day: 'numeric' }).format(date)
}

export function formatMultiplier(milli = 1000): string {
  const value = Number.isFinite(milli) && milli > 0 ? milli / 1000 : 1
  return `×${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(value)}`
}

export function billingCycleLabel(snapshot: Pick<Snapshot, 'billing_mode' | 'period_months'>, anchorDay: number, timezone: string): string {
  const months = Number.isInteger(snapshot.period_months) && (snapshot.period_months ?? 0) > 0 ? snapshot.period_months as number : 1
  const day = Number.isInteger(anchorDay) && anchorDay >= 1 && anchorDay <= 31 ? anchorDay : 1
  const zone = timezone || 'UTC'
  if (snapshot.billing_mode && snapshot.billing_mode !== 'monthly_anchor') return `按账期重置（${zone}）`
  if (months === 1) return `每月 ${day} 日重置（${zone}）`
  return `每 ${months} 个月按起算日重置（每月 ${day} 日，${zone}）`
}
