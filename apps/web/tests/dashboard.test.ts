import test from 'node:test'
import assert from 'node:assert/strict'
import { billingCycleLabel, dailyRange, formatBytes, formatMultiplier, isSecureLocation, loadResource, loadViewer, planScopeLabels } from '../src/lib/dashboard.ts'

test('public HTTP never requests a session', async () => {
  const viewer = await loadViewer('http:', '203.0.113.7', () => { throw new Error('network was used') })
  assert.deepEqual(viewer, { kind: 'preview' })
  assert.equal(isSecureLocation('http:', 'localhost'), true)
  assert.equal(isSecureLocation('https:', 'example.test'), true)
  assert.equal(isSecureLocation('http:', 'example.test'), false)
})

test('safe origin distinguishes disabled auth, guest and signed-in user', async () => {
  assert.deepEqual(await loadViewer('https:', 'example.test', async () => new Response(null, { status: 404 })), { kind: 'preview' })
  assert.deepEqual(await loadViewer('https:', 'example.test', async () => new Response(null, { status: 401 })), { kind: 'guest' })
  const viewer = await loadViewer('https:', 'example.test', async () => Response.json({ id: 'u1', email: 'a@example.test', status: 'active', timezone: 'UTC', roles: ['user'], permissions: ['dashboard.read'] }))
  assert.equal(viewer.kind, 'signed-in')
  if (viewer.kind === 'signed-in') assert.equal(viewer.user.email, 'a@example.test')
})

test('daily range uses inclusive UTC dates and stays within API limit', () => {
  assert.deepEqual(dailyRange(new Date('2026-09-26T23:00:00Z'), 14), { from: '2026-09-13', to: '2026-09-26' })
  assert.equal(formatBytes(0), '0 B')
  assert.equal(formatBytes(1073741824), '1 GB')
})

test('independent dashboard resources preserve empty and permission states', async () => {
  assert.deepEqual(await loadResource('/api/v1/my/membership', async () => new Response(null, { status: 404 })), { kind: 'empty' })
  assert.deepEqual(await loadResource('/api/v1/my/usage/daily', async () => new Response(null, { status: 403 })), { kind: 'error', message: '当前账户无权查看此数据。' })
})

test('plan display describes anchored billing and multiplier precisely', () => {
  assert.equal(billingCycleLabel({ billing_mode: 'monthly_anchor', period_months: 1 }, 26, 'Asia/Shanghai'), '每月 26 日重置（Asia/Shanghai）')
  assert.equal(billingCycleLabel({ billing_mode: 'monthly_anchor', period_months: 3 }, 31, 'UTC'), '每 3 个月按起算日重置（每月 31 日，UTC）')
  assert.equal(formatMultiplier(1250), '×1.25')
  assert.equal(formatMultiplier(1000), '×1')
})

test('plan scope labels resolve authorized resource groups and lines in snapshot order', () => {
  const result = planScopeLabels(
    { resource_group_ids: ['group-jp', 'group-hk'], line_ids: ['line-ai', 'line-direct'] },
    [
      { id: 'node-1', group_id: 'group-hk', group_code: 'RFC.HKT1', name: '香港中转' },
      { id: 'node-2', group_id: 'group-jp', group_code: 'RFC.JPT1', name: '日本落地' },
    ],
    [
      { id: 'line-direct', name: '直连日本' },
      { id: 'line-ai', name: 'AI 美国专线' },
    ],
  )
  assert.deepEqual(result, {
    resourceGroups: ['RFC.JPT1', 'RFC.HKT1'],
    lines: ['AI 美国专线', '直连日本'],
  })
})
