import test from 'node:test'
import assert from 'node:assert/strict'
import { dailyRange, formatBytes, isSecureLocation, loadResource, loadViewer } from '../src/lib/dashboard.ts'

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
