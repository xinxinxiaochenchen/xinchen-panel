import test from 'node:test'
import assert from 'node:assert/strict'
import { forwardDraftFromRecord, forwardDraftPayload } from '../src/features/catalog/forwardDraft.ts'

test('forward edit only sends a validated name and preserves immutable endpoints', () => {
  const draft = forwardDraftFromRecord({ name: '  Web  ', ingress_port: 10443, target_port: 443 })
  assert.deepEqual(draft, { name: '  Web  ' })
  assert.deepEqual(forwardDraftPayload(draft), { name: 'Web' })
  assert.throws(() => forwardDraftPayload({ name: '   ' }), /名称/)
  assert.throws(() => forwardDraftPayload({ name: 'x'.repeat(101) }), /100/)
})
