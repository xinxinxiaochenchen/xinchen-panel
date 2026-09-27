import test from 'node:test'
import assert from 'node:assert/strict'
import { loadAllCatalogPages, loadCatalogPage, mutateCatalog, type NodeRecord } from '../src/lib/catalog.ts'
import { customRolePermissions, loadRoleDirectory } from '../src/lib/admin.ts'
import { lineEditPayload, lineEditPath, lineTogglePath } from '../src/features/catalog/lineAccess.ts'
import { draftLinePayload } from '../src/features/catalog/lineDraft.ts'

test('multi-hop draft payload preserves hop roles and cannot be enabled', () => {
  assert.deepEqual(draftLinePayload(' Japan relay ', ['ingress-id', 'relay-id', 'egress-id'], 20, 3), {
    name: 'Japan relay', enabled: false, priority: 20, weight: 3,
    hops: [
      { position: 0, node_id: 'ingress-id', role: 'ingress' },
      { position: 1, node_id: 'relay-id', role: 'relay' },
      { position: 2, node_id: 'egress-id', role: 'egress' },
    ],
  })
  assert.throws(() => draftLinePayload('Draft', ['same', 'same'], 100, 1), /不同节点/)
  assert.throws(() => draftLinePayload('Draft', ['only-one'], 100, 1), /2 至 8/)
})

test('catalog page sends opaque cursor without exposing another resource path', async () => {
  const calls: string[] = []
  const request = async (input: RequestInfo | URL) => {
    calls.push(String(input))
    return Response.json({ items: [{ id: 'node-1', name: 'Japan' }], next_cursor: 'a/b+==' })
  }
  const result = await loadCatalogPage<NodeRecord>('/api/v1/nodes', 'a/b+==', request as typeof fetch)
  assert.equal(calls[0], '/api/v1/nodes?limit=50&cursor=a%2Fb%2B%3D%3D')
  assert.equal(result.kind, 'ready')
  if (result.kind === 'ready') assert.equal(result.data.next_cursor, 'a/b+==')
})

test('catalog page distinguishes empty results from denied access', async () => {
  const empty = await loadCatalogPage<NodeRecord>('/api/v1/nodes', null, async () => Response.json({ items: [], next_cursor: null }))
  assert.deepEqual(empty, { kind: 'empty' })
  const denied = await loadCatalogPage<NodeRecord>('/api/v1/nodes', null, async () => new Response(null, { status: 403 }))
  assert.equal(denied.kind, 'error')
  if (denied.kind === 'error') assert.match(denied.message, /无权/)
})

test('catalog mutation requires CSRF and reports server validation', async () => {
  const absent = await mutateCatalog('/api/v1/lines', 'POST', { name: 'Japan' }, '', async () => { throw new Error('request sent without CSRF') })
  assert.equal(absent.kind, 'error')
  let header = ''
  const result = await mutateCatalog('/api/v1/lines', 'POST', { name: 'Japan' }, 'csrf-123', async (_input, init) => {
    header = new Headers(init?.headers).get('X-CSRF-Token') ?? ''
    return Response.json({ error: { code: 'VALIDATION_ERROR', message: 'node_id: expected UUID' } }, { status: 422 })
  })
  assert.equal(header, 'csrf-123')
  assert.deepEqual(result, { kind: 'error', message: 'node_id: expected UUID' })
})

test('role directory loads custom RBAC permissions and role assignment can use PUT', async () => {
  const directory = await loadRoleDirectory(async () => Response.json({ roles: [{ code: 'support' }], permissions: [{ code: 'usage.read' }] }))
  assert.equal(directory.kind, 'ready')
  if (directory.kind === 'ready') assert.equal(directory.data.roles[0].code, 'support')
  let method = ''
  const result = await mutateCatalog('/api/v1/admin/users/member/roles', 'PUT', { role_codes: ['support'] }, 'csrf', async (_input, init) => {
    method = init?.method ?? ''
    return new Response(null, { status: 204 })
  })
  assert.equal(method, 'PUT')
  assert.equal(result.kind, 'ready')
})

test('custom role permission choices exclude system role management', () => {
  assert.deepEqual(customRolePermissions([
    { code: 'roles.write', description: 'Manage roles' },
    { code: 'usage.read', description: 'Read usage' },
  ]), [{ code: 'usage.read', description: 'Read usage' }])
})

test('catalog option loader follows all cursor pages', async () => {
  const calls: string[] = []
  const result = await loadAllCatalogPages<NodeRecord>('/api/v1/nodes', async (input) => {
    calls.push(String(input))
    return Response.json(calls.length === 1 ? { items: [{ id: 'one' }], next_cursor: 'next' } : { items: [{ id: 'two' }], next_cursor: null })
  })
  assert.equal(result.kind, 'ready')
  if (result.kind === 'ready') assert.deepEqual(result.data.map((item) => item.id), ['one', 'two'])
  assert.equal(calls.length, 2)
})

test('line toggle uses shared administrator route and owner scoped route', () => {
  const shared = { id: 'shared-id', owner_user_id: null }
  const own = { id: 'own-id', owner_user_id: 'member-id' }
  const admin = { id: 'admin-id', permissions: ['lines.write', 'lines.write.self'] }
  const member = { id: 'member-id', permissions: ['lines.write.self'] }
  assert.equal(lineTogglePath(shared, admin), '/api/v1/admin/lines/shared-id')
  assert.equal(lineTogglePath(own, member), '/api/v1/lines/own-id')
  assert.equal(lineTogglePath(shared, member), null)
  assert.equal(lineTogglePath({ id: 'foreign-id', owner_user_id: 'other-id' }, admin), null)
  assert.equal(lineTogglePath({ id: 'line/a', owner_user_id: null }, admin), '/api/v1/admin/lines/line%2Fa')
})

test('line edit uses the same permission scope as toggle', () => {
  const shared = { id: 'shared-id', owner_user_id: null }
  const own = { id: 'own-id', owner_user_id: 'member-id' }
  const admin = { id: 'admin-id', permissions: ['lines.write', 'lines.write.self'] }
  const member = { id: 'member-id', permissions: ['lines.write.self'] }
  assert.equal(lineEditPath(shared, admin), '/api/v1/admin/lines/shared-id')
  assert.equal(lineEditPath(own, member), '/api/v1/lines/own-id')
  assert.equal(lineEditPath(shared, member), null)
  assert.equal(lineEditPath({ id: 'foreign-id', owner_user_id: 'other-id' }, admin), null)
})

test('line edit payload omits billing multiplier for member-owned lines', () => {
  const draft = { name: ' Japan daily ', priority: 20, weight: 4, tags: 'jp, daily, jp', multiplier_milli: 1250 }
  assert.deepEqual(lineEditPayload(draft, true), {
    name: 'Japan daily', priority: 20, weight: 4, tags: ['jp', 'daily'], multiplier_milli: 1250,
  })
  assert.deepEqual(lineEditPayload(draft, false), {
    name: 'Japan daily', priority: 20, weight: 4, tags: ['jp', 'daily'],
  })
  assert.deepEqual(lineEditPayload({ ...draft, multiplier_milli: null }, true), {
    name: 'Japan daily', priority: 20, weight: 4, tags: ['jp', 'daily'],
  })
})
