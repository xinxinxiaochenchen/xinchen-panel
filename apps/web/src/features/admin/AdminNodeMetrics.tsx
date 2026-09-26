import { useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import type { NodeMetricsRecord } from '../../lib/catalog'
import { formatMetricBytes, metricFreshnessLabel } from './nodeMetrics'

export function AdminNodeMetrics({ nodeID }: { nodeID: string }) {
  const [data, setData] = useState<NodeMetricsRecord | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [generation, setGeneration] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    let timer: number | undefined
    setData(null)
    setError('')
    setLoading(true)
    async function load() {
      try {
        const response = await fetch(`/api/v1/admin/nodes/${nodeID}/metrics`, { credentials: 'same-origin', cache: 'no-store', signal: controller.signal })
        if (!response.ok) throw new Error(`指标请求失败（${response.status}）`)
        const value = await response.json() as NodeMetricsRecord
        if (!controller.signal.aborted) { setData(value); setError(''); setLoading(false) }
      } catch (caught) {
        if (!controller.signal.aborted) { setError(caught instanceof Error ? caught.message : '指标暂不可用。'); setLoading(false) }
      } finally {
        if (!controller.signal.aborted) timer = window.setTimeout(() => void load(), 15000)
      }
    }
    void load()
    return () => { controller.abort(); window.clearTimeout(timer) }
  }, [nodeID, generation])

  const metrics = data?.metrics
  return <div className="admin-node-metrics" aria-live="polite">
    <div className="admin-node-metrics-head"><strong>{metricFreshnessLabel(data)}</strong><button className="refresh-button" type="button" onClick={() => setGeneration((value) => value + 1)}><RefreshCw size={13} />刷新</button></div>
    {loading && <span className="catalog-form-note">正在读取 Agent 指标…</span>}
    {error && <span className="catalog-page-error" role="alert">{error}</span>}
    {metrics && <><div className="admin-node-metrics-grid">
      <span><small>CPU</small><strong>{metrics.cpu_pct.toFixed(1)}%</strong></span>
      <span><small>已用内存</small><strong>{formatMetricBytes(metrics.memory_used_bytes)}</strong></span>
      <span><small>连接数</small><strong>{metrics.connections}</strong></span>
      <span><small>运行时间</small><strong>{Math.floor(metrics.uptime_seconds / 3600)} 小时</strong></span>
      <span><small>网卡接收累计</small><strong>{formatMetricBytes(metrics.rx_bytes)}</strong></span>
      <span><small>网卡发送累计</small><strong>{formatMetricBytes(metrics.tx_bytes)}</strong></span>
    </div><small className="catalog-form-note">Agent 引擎：{metrics.engine_status} · 采集时间：{new Date(metrics.observed_at).toLocaleString('zh-CN')}。网卡累计流量与套餐计费流量分开统计。</small></>}
  </div>
}
