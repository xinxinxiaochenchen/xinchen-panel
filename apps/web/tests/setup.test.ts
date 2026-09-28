import test from 'node:test'
import assert from 'node:assert/strict'
import { initializePanel, validateInitialSetup } from '../src/lib/setup.ts'

const valid = { token: 'A'.repeat(43), email: 'owner@example.test', password: 'long-initial-password', confirmation: 'long-initial-password' }

test('setup validates confirmation and UTF-8 password limits before sending', async () => {
  assert.equal(validateInitialSetup(valid), '')
  assert.match(validateInitialSetup({ ...valid, confirmation: 'different-password' }), /一致/)
  assert.match(validateInitialSetup({ ...valid, password: '密'.repeat(25), confirmation: '密'.repeat(25) }), /72/)
  let requests = 0
  await assert.rejects(initializePanel({ ...valid, confirmation: 'different' }, async () => { requests++; return Response.json({}) }))
  assert.equal(requests, 0)
})

test('setup sends private values only in the request body and reports completion races', async () => {
  const request = async (path: string | URL | Request, options?: RequestInit) => {
    assert.equal(path, '/api/v1/setup')
    assert.equal(options?.credentials, 'same-origin')
    assert.equal(options?.cache, 'no-store')
    assert.deepEqual(JSON.parse(String(options?.body)), { token: valid.token, email: valid.email, password: valid.password })
    return new Response(null, { status: 201 })
  }
  await initializePanel(valid, request)
  await assert.rejects(initializePanel(valid, async () => new Response(null, { status: 403 })), /凭证/)
  await assert.rejects(initializePanel(valid, async () => Response.json({ error: { code: 'SETUP_COMPLETE' } }, { status: 409 })), /已完成/)
})
