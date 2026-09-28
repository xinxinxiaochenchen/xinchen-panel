import { useEffect, useState } from 'react'
import { Activity, ArrowDownLeft, ArrowUpRight, CalendarDays, CircleUserRound, Gauge, GitBranch, Layers3, RefreshCw, Route, ShieldCheck, Wallet } from 'lucide-react'
import { billingCycleLabel, dailyRange, formatBytes, formatDate, formatMultiplier, loadResource, planScopeLabels, type DailyUsage, type Membership, type PlanScopeLabels, type Resource, type ScopeLine, type ScopeNode, type Usage, type User } from '../../lib/dashboard'
import { loadAllCatalogPages } from '../../lib/catalog'

type DailyResponse = { items: DailyUsage[] }
const loading = { kind: 'loading' } as const

function DataStatus({ state, empty }: { state: Resource<unknown>; empty: string }) {
  if (state.kind === 'loading') return <div className="data-status" role="status">正在加载实时数据…</div>
  if (state.kind === 'empty') return <div className="data-status">{empty}</div>
  if (state.kind === 'error') return <div className="data-status data-error" role="alert">{state.message}</div>
  return null
}

function MembershipCard({ state, usage }: { state: Resource<Membership>; usage: Resource<Usage> }) {
  return <article className="dashboard-card membership-card">
    <div className="dashboard-card-heading"><span><Wallet size={18} /> 当前套餐</span><span className="live-tag">LIVE DATA</span></div>
    {state.kind === 'ready' ? <>
      <strong className="plan-name">{state.data.snapshot.plan_name || '未命名套餐'}</strong>
      <span className="plan-status"><span />{state.data.status === 'active' ? '使用中' : state.data.status}</span>
      <div className="card-divider" />
      <div className="detail-row"><span>开始日期</span><strong>{formatDate(state.data.starts_at)}</strong></div>
      <div className="detail-row"><span>有效期至</span><strong>{formatDate(state.data.ends_at)}</strong></div>
      <div className="detail-row"><span>账期</span><strong>{billingCycleLabel(state.data.snapshot, state.data.anchor_day, state.data.timezone)}</strong></div>
      <div className="detail-row"><span>下次重置</span><strong>{usage.kind === 'ready' ? formatDate(usage.data.ends_at) : '加载中…'}</strong></div>
    </> : <DataStatus state={state} empty="当前没有生效的套餐。" />}
  </article>
}

function PlanScopeCard({ state, scope }: { state: Resource<Membership>; scope: Resource<PlanScopeLabels> }) {
  const snapshot = state.kind === 'ready' ? state.data.snapshot : null
  const limits = snapshot?.limits ?? {}
  return <article className="dashboard-card scope-card">
    <div className="dashboard-card-heading"><span><Layers3 size={18} /> 套餐范围与限制</span><span className="live-tag">授权快照</span></div>
    {snapshot ? <>
      <div className="scope-grid">
        <div><span><Layers3 size={15} />节点范围</span><strong>{snapshot.resource_group_ids?.length ?? 0} 个资源域</strong></div>
        <div><span><Route size={15} />线路范围</span><strong>{snapshot.line_ids?.length ?? 0} 条共享线路</strong></div>
        <div><span><Gauge size={15} />默认倍率</span><strong>{formatMultiplier(snapshot.default_multiplier_milli)}</strong></div>
        <div><span><GitBranch size={15} />最大跳数</span><strong>{limits.max_hops ?? 1} 跳</strong></div>
      </div>
      {scope.kind === 'ready' && (scope.data.resourceGroups.length > 0 || scope.data.lines.length > 0) && <div className="scope-detail-list">
        {scope.data.resourceGroups.length > 0 && <div><span>可用资源域</span><p>{scope.data.resourceGroups.map((label) => <em key={label}>{label}</em>)}</p></div>}
        {scope.data.lines.length > 0 && <div><span>可用线路</span><p>{scope.data.lines.map((label) => <em key={label}>{label}</em>)}</p></div>}
      </div>}
      <div className="scope-limits">
        <div><span>每节点转发规则</span><strong>{limits.max_forward_rules_per_node ?? 0}</strong></div>
        <div><span>代理候选线路</span><strong>{limits.max_proxy_lines ?? 1}</strong></div>
        <div><span>订阅数量</span><strong>{limits.max_subscriptions ?? 0}</strong></div>
        <div><span>自有线路</span><strong>{limits.allow_custom_lines ? `${limits.max_custom_lines ?? 0} 条` : '不允许'}</strong></div>
      </div>
    </> : <DataStatus state={state} empty="当前没有可用的套餐授权。" />}
  </article>
}

function UsageCard({ state }: { state: Resource<Usage> }) {
  const data = state.kind === 'ready' ? state.data : null
  const percent = data ? Math.min(100, Math.max(0, data.usage_percent)) : 0
  return <article className="dashboard-card usage-card">
    <div className="dashboard-card-heading"><span><Activity size={18} /> 本期流量</span>{data && <span className="period-label">{formatDate(data.starts_at)} — {formatDate(data.ends_at)}</span>}</div>
    {data ? <>
      <div className="usage-total"><div><span>已计费流量</span><strong>{formatBytes(data.charged_bytes)}</strong></div><span className="usage-ratio">{percent.toFixed(1)}%</span></div>
      <div className="usage-progress" role="progressbar" aria-label="套餐流量使用率" aria-valuenow={percent} aria-valuemin={0} aria-valuemax={100}><span style={{ width: `${percent}%` }} /></div>
      <div className="usage-legend"><span>本期配额 {formatBytes(data.quota_bytes)}</span><span>剩余 {formatBytes(data.remaining_bytes)}</span></div>
      <div className="usage-metrics">
        <div><ArrowUpRight size={17} /><span>上传</span><strong>{formatBytes(data.uploaded_bytes)}</strong></div>
        <div><ArrowDownLeft size={17} /><span>下载</span><strong>{formatBytes(data.downloaded_bytes)}</strong></div>
        <div><ShieldCheck size={17} /><span>当前可用</span><strong>{formatBytes(data.available_bytes)}</strong></div>
      </div>
      {data.reserved_bytes > 0 && <p className="usage-note">已预留 {formatBytes(data.reserved_bytes)}，当前可用流量已扣除预留。</p>}
    </> : <DataStatus state={state} empty="当前没有可显示的计费周期。" />}
  </article>
}

function DailyCard({ state, from, to }: { state: Resource<DailyResponse>; from: string; to: string }) {
  const items = state.kind === 'ready' ? state.data.items ?? [] : []
  const values = new Map(items.map((item) => [item.date, item.charged_bytes]))
  const days: { date: string; bytes: number }[] = []
  const cursor = new Date(`${from}T00:00:00Z`)
  while (cursor.toISOString().slice(0, 10) <= to) {
    const date = cursor.toISOString().slice(0, 10)
    days.push({ date, bytes: values.get(date) ?? 0 })
    cursor.setUTCDate(cursor.getUTCDate() + 1)
  }
  const max = Math.max(1, ...days.map((day) => day.bytes))
  const total = days.reduce((sum, day) => sum + day.bytes, 0)
  return <article className="dashboard-card daily-card">
    <div className="dashboard-card-heading"><span><CalendarDays size={18} /> 最近 14 天</span><span className="period-label">按 UTC 日期统计</span></div>
    {state.kind === 'ready' ? <>
      <div className="daily-summary"><span>已计费流量</span><strong>{formatBytes(total)}</strong></div>
      {items.length === 0 ? <div className="data-status">这段时间暂无流量记录。</div> : <div className="daily-chart" role="img" aria-label={`从 ${from} 到 ${to} 的每日计费流量`}>
        {days.map((day) => <div className="daily-bar" key={day.date} title={`${day.date}：${formatBytes(day.bytes)}`}><span style={{ height: `${Math.max(day.bytes > 0 ? 6 : 2, day.bytes / max * 100)}%` }} /></div>)}
      </div>}
      <div className="chart-axis"><span>{from.slice(5)}</span><span>{to.slice(5)}</span></div>
    </> : <DataStatus state={state} empty="这段时间暂无流量记录。" />}
  </article>
}

export function DashboardHome({ user }: { user: User }) {
  const [generation, setGeneration] = useState(0)
  const [membership, setMembership] = useState<Resource<Membership>>(loading)
  const [usage, setUsage] = useState<Resource<Usage>>(loading)
  const [daily, setDaily] = useState<Resource<DailyResponse>>(loading)
  const [scope, setScope] = useState<Resource<PlanScopeLabels>>(loading)
  const { from, to } = dailyRange(new Date(), 14)

  useEffect(() => {
    let active = true
    setMembership(loading)
    setUsage(loading)
    setDaily(loading)
    setScope(loading)
    const read = async <T,>(path: string, update: (value: Resource<T>) => void) => {
      const result = await loadResource<T>(path)
      if (active) update(result)
    }
    void (async () => {
      const result = await loadResource<Membership>('/api/v1/my/membership')
      if (!active) return
      setMembership(result)
      if (result.kind !== 'ready') return
      const [nodes, lines] = await Promise.all([
        loadAllCatalogPages<ScopeNode>('/api/v1/nodes'),
        loadAllCatalogPages<ScopeLine>('/api/v1/lines'),
      ])
      if (!active) return
      if (nodes.kind === 'error') return setScope({ kind: 'error', message: nodes.message })
      if (lines.kind === 'error') return setScope({ kind: 'error', message: lines.message })
      setScope({ kind: 'ready', data: planScopeLabels(result.data.snapshot, nodes.kind === 'ready' ? nodes.data : [], lines.kind === 'ready' ? lines.data : []) })
    })()
    void read('/api/v1/my/usage/current', setUsage)
    void read(`/api/v1/my/usage/daily?${new URLSearchParams({ from, to })}`, setDaily)
    return () => { active = false }
  }, [user.id, generation, from, to])

  return <div className="dashboard-home">
    <div className="dashboard-intro"><div><span className="section-overline">PERSONAL OVERVIEW</span><h1>你好，{user.email.split('@')[0]}</h1><p>账户、套餐与真实流量都在这里。</p></div><button className="refresh-button" type="button" onClick={() => setGeneration((value) => value + 1)}><RefreshCw size={16} />刷新数据</button></div>
    <div className="account-strip"><span className="account-avatar"><CircleUserRound size={23} /></span><div><small>当前账户</small><strong>{user.email}</strong></div><span className="account-active"><span />{user.status === 'active' ? '账户正常' : user.status}</span></div>
    <div className="dashboard-grid"><MembershipCard state={membership} usage={usage} /><PlanScopeCard state={membership} scope={scope} /><UsageCard state={usage} /><DailyCard state={daily} from={from} to={to} /></div>
    <p className="dashboard-footnote">数据来自当前账户的实时接口；账期流量与最近 14 天的 UTC 日统计口径可能不同。</p>
  </div>
}
