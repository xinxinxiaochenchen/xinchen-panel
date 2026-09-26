export function formatMetricBytes(value: number): string {
  if (!Number.isFinite(value) || value < 0) return '—'
  if (value < 1024) return `${value} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let unit = -1
  do { amount /= 1024; unit += 1 } while (amount >= 1024 && unit < units.length - 1)
  return `${Number(amount.toFixed(1))} ${units[unit]}`
}

export function metricFreshnessLabel(value: { fresh: boolean; agent_status: string } | null): string {
  if (!value) return '暂无心跳数据'
  if (value.agent_status === 'unknown') return '暂无心跳数据'
  if (value.fresh) return '实时数据'
  if (value.agent_status === 'online') return '历史数据 · Agent 在线'
  return `历史数据 · 当前${value.agent_status === 'revoked' ? '已撤销' : '离线'}`
}
