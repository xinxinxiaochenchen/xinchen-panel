import test from 'node:test'
import assert from 'node:assert/strict'
import { issueAgentEnrollmentToken, revokeAgent } from '../src/features/admin/agentEnrollment.ts'

test('Agent enrollment reauth sends password with CSRF to the node scoped endpoint', async () => {
  let path = ''
  let body = ''
  let csrf = ''
  const response = await issueAgentEnrollmentToken('node-id', 'current password', 'csrf-value', async (input, init) => {
    path = String(input)
    body = String(init?.body)
    csrf = new Headers(init?.headers).get('X-CSRF-Token') ?? ''
    return Response.json({ token: 'once-only', expires_at: '2026-09-26T15:00:00Z' }, { status: 201 })
  })
  assert.equal(path, '/api/v1/admin/nodes/node-id/agent-enrollment')
  assert.deepEqual(JSON.parse(body), { password: 'current password' })
  assert.equal(csrf, 'csrf-value')
  assert.equal(response.kind, 'ready')
  if (response.kind === 'ready') assert.equal(response.data.token, 'once-only')
})

test('Agent enrollment does not send a request without password or CSRF', async () => {
  const noRequest = async () => { throw new Error('request was sent') }
  assert.equal((await issueAgentEnrollmentToken('node', '', 'csrf', noRequest)).kind, 'error')
  assert.equal((await issueAgentEnrollmentToken('node', 'password', '', noRequest)).kind, 'error')
})

test('Agent revoke sends only the status mutation with CSRF', async () => {
  let path = ''
  let method = ''
  let body = ''
  const response = await revokeAgent('node/id', 'csrf-value', async (input, init) => {
    path = String(input)
    method = init?.method ?? ''
    body = String(init?.body)
    return new Response(null, { status: 204 })
  })
  assert.equal(path, '/api/v1/admin/nodes/node%2Fid/agent')
  assert.equal(method, 'PATCH')
  assert.deepEqual(JSON.parse(body), { status: 'revoked' })
  assert.equal(response.kind, 'ready')
})
