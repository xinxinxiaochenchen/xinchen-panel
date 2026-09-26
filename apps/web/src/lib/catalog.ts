export type NodeRecord = {
  id: string
  group_code: string
  name: string
  region: string
  host: string
  public_ip: string | null
  proxy_port: number | null
  capabilities: string[]
  bandwidth_bps: number | null
  multiplier_milli: number
  tags: string[]
  enabled: boolean
  agent_status: string
  last_seen_at: string | null
}

export type NodeMetricsRecord = {
  node_id: string
  agent_status: string
  last_seen_at: string | null
  fresh: boolean
  metrics: null | {
    observed_at: string
    uptime_seconds: number
    cpu_pct: number
    memory_used_bytes: number
    rx_bytes: number
    tx_bytes: number
    connections: number
    engine_status: string
  }
}

export type LineRecord = {
  id: string
  owner_user_id: string | null
  name: string
  enabled: boolean
  priority: number
  weight: number
  multiplier_milli: number | null
  tags: string[]
  hops: { position: number; node_id: string; role: string }[]
  created_at: string
  updated_at: string
}

export type ForwardRecord = {
  id: string
  user_id: string
  name: string
  ingress_node_id: string
  ingress_port: number
  target_node_id: string | null
  target_host: string | null
  target_port: number
  line_id: string | null
  protocol: 'TCP' | 'UDP' | 'BOTH'
  enabled: boolean
  apply_status: string
  created_at: string
  updated_at: string
}

export type ProxyAccessRecord = { id: string; user_id: string; line_id: string; name: string; enabled: boolean; apply_status: string; created_at: string; updated_at: string }
export type RoutingProfileRecord = { id: string; user_id: string; name: string; fallback_kind: string; fallback_line_id: string | null; enabled: boolean; revision: number; created_at: string; updated_at: string }
export type SubscriptionRecord = { id: string; user_id: string; name: string; name_template: string; proxy_access_ids: string[]; routing_profile_id?: string | null; enabled: boolean; created_at: string; updated_at: string }
export type SubscriptionCreateResult = { subscription: SubscriptionRecord; token: string; path: string }

export type CatalogPage<T> = { items: T[]; next_cursor: string | null }
export type CatalogResource<T> =
  | { kind: 'loading' }
  | { kind: 'empty' }
  | { kind: 'ready'; data: CatalogPage<T> }
  | { kind: 'error'; message: string }

export type MutationResource<T> = { kind: 'ready'; data: T } | { kind: 'error'; message: string }

export async function loadCatalogPage<T>(path: string, cursor: string | null, request: typeof fetch = fetch): Promise<CatalogResource<T>> {
  const query = new URLSearchParams({ limit: '50' })
  if (cursor) query.set('cursor', cursor)
  try {
    const response = await request(`${path}?${query.toString()}`, { credentials: 'same-origin', cache: 'no-store' })
    if (response.status === 404) return { kind: 'empty' }
    if (response.status === 403) return { kind: 'error', message: '当前账户无权查看此资源。' }
    if (response.status === 401) return { kind: 'error', message: '登录已过期，请重新登录。' }
    if (!response.ok) return { kind: 'error', message: `请求失败（${response.status}）` }
    const data = await response.json() as CatalogPage<T>
    if (!Array.isArray(data.items)) return { kind: 'error', message: '服务端返回了无效列表。' }
    return data.items.length === 0 && !data.next_cursor ? { kind: 'empty' } : { kind: 'ready', data }
  } catch {
    return { kind: 'error', message: '资源暂不可用，请稍后重试。' }
  }
}

export async function loadAllCatalogPages<T>(path: string, request: typeof fetch = fetch): Promise<{ kind: 'ready'; data: T[] } | { kind: 'empty' } | { kind: 'error'; message: string }> {
  const items: T[] = []
  let cursor: string | null = null
  for (let page = 0; page < 100; page += 1) {
    const result: CatalogResource<T> = await loadCatalogPage<T>(path, cursor, request)
    if (result.kind === 'error') return result
    if (result.kind === 'empty') return items.length ? { kind: 'ready', data: items } : result
    if (result.kind !== 'ready') return { kind: 'error', message: '资源列表状态无效。' }
    items.push(...result.data.items)
    cursor = result.data.next_cursor
    if (!cursor) return items.length ? { kind: 'ready', data: items } : { kind: 'empty' }
  }
  return { kind: 'error', message: '资源列表分页超过安全上限。' }
}

export async function mutateCatalog<T>(path: string, method: 'POST' | 'PATCH' | 'DELETE', body: unknown, csrf: string, request: typeof fetch = fetch): Promise<MutationResource<T>> {
  if (!csrf) return { kind: 'error', message: '安全令牌不可用，请刷新页面后重试。' }
  try {
    const response = await request(path, { method, credentials: 'same-origin', cache: 'no-store', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: method === 'DELETE' ? undefined : JSON.stringify(body) })
    if (!response.ok) {
      const payload = await response.json().catch(() => null) as { error?: { message?: string } } | null
      return { kind: 'error', message: payload?.error?.message || `请求失败（${response.status}）` }
    }
    if (response.status === 204) return { kind: 'ready', data: undefined as T }
    return { kind: 'ready', data: await response.json() as T }
  } catch {
    return { kind: 'error', message: '请求暂不可用，请稍后重试。' }
  }
}

export function nodeStatusLabel(status: string): string {
  return ({ online: '在线', offline: '离线', revoked: '已撤销', unknown: '未接入' } as Record<string, string>)[status] ?? status
}

export function capabilityLabel(capability: string): string {
  return capability === 'proxy' ? '代理出口' : capability === 'forward' ? '中转入口' : capability
}
