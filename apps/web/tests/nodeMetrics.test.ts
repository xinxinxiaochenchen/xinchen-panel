import test from 'node:test'
import assert from 'node:assert/strict'
import { formatLatency, formatMetricBytes, metricFreshnessLabel } from '../src/features/admin/nodeMetrics.ts'

test('node metric display distinguishes unknown and stale observations', () => {
  assert.equal(metricFreshnessLabel(null), '暂无心跳数据')
  assert.equal(metricFreshnessLabel({ fresh: false, agent_status: 'unknown' }), '暂无心跳数据')
  assert.equal(metricFreshnessLabel({ fresh: false, agent_status: 'offline' }), '历史数据 · 当前离线')
  assert.equal(metricFreshnessLabel({ fresh: false, agent_status: 'online' }), '历史数据 · Agent 在线')
  assert.equal(metricFreshnessLabel({ fresh: true, agent_status: 'online' }), '实时数据')
})

test('node metric byte counters remain distinct from billed traffic', () => {
  assert.equal(formatMetricBytes(0), '0 B')
  assert.equal(formatMetricBytes(1048576), '1 MiB')
  assert.equal(formatMetricBytes(1610612736), '1.5 GiB')
})

test('node latency is a measured RTT and unknown latency stays blank', () => {
  assert.equal(formatLatency(null), '—')
  assert.equal(formatLatency(17), '17 ms')
})
