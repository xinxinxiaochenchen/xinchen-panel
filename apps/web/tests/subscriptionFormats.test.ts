import test from 'node:test'
import assert from 'node:assert/strict'
import { subscriptionPreviewPath, subscriptionTokenPath, subscriptionURLPath } from '../src/features/catalog/subscriptionFormats.ts'

test('subscription format applies to URL reveal, preview and rotated token', () => {
  assert.equal(subscriptionURLPath('sub-1', 'surge'), '/api/v1/subscriptions/sub-1/url?format=surge')
  assert.equal(subscriptionPreviewPath('sub-1', 'sing-box'), '/api/v1/subscriptions/sub-1/preview?format=sing-box')
  assert.equal(subscriptionTokenPath('abc', 'mihomo'), '/sub/abc/mihomo')
  assert.equal(subscriptionTokenPath('abc', 'surge'), '/sub/abc/surge')
})
