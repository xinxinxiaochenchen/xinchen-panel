import test from 'node:test'
import assert from 'node:assert/strict'
import { readCSRFToken, sessionTransportLabel } from '../src/lib/browserSession.ts'
import { copyText } from '../src/lib/clipboard.ts'

test('CSRF reader accepts the configured HTTP or HTTPS cookie names', () => {
  assert.equal(readCSRFToken('theme=dark; control_csrf=http-token', 'http:'), 'http-token')
  assert.equal(readCSRFToken('theme=dark;__Host-control_csrf=https-token', 'https:'), 'https-token')
  assert.equal(readCSRFToken('control_csrf=old; __Host-control_csrf=new', 'https:'), 'new')
  assert.equal(readCSRFToken('__Host-control_csrf=old; control_csrf=new', 'http:'), 'new')
  assert.equal(readCSRFToken('other_control_csrf=wrong; theme=dark', 'http:'), '')
})

test('HTTP sessions are not described as encrypted', () => {
  assert.equal(sessionTransportLabel('http:'), 'HTTP 会话')
  assert.equal(sessionTransportLabel('https:'), 'HTTPS 加密连接')
})

test('clipboard copies with modern API where available', async () => {
  let copied = ''
  const result = await copyText('token', { writeText: async (value: string) => { copied = value } }, () => { throw new Error('fallback should not run') })
  assert.equal(result, true)
  assert.equal(copied, 'token')
})

test('HTTP clipboard absence and API rejection both use the selection fallback', async () => {
  let copied = ''
  const fallback = (value: string) => { copied = value; return true }
  assert.equal(await copyText('http-subscription', undefined, fallback), true)
  assert.equal(copied, 'http-subscription')
  assert.equal(await copyText('agent-token', { writeText: async () => { throw new Error('denied') } }, fallback), true)
  assert.equal(copied, 'agent-token')
  assert.equal(await copyText('value', undefined, () => false), false)
  assert.equal(await copyText('value', undefined, () => { throw new Error('no DOM') }), false)
})
