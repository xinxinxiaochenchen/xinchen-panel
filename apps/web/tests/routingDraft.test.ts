import test from 'node:test'
import assert from 'node:assert/strict'
import { profileDraftFromRecord, profileDraftPayload, ruleDraftFromRecord, ruleDraftPayload } from '../src/features/catalog/routingDraft.ts'

test('editing a routing profile can replace a line fallback with direct', () => {
  const draft = profileDraftFromRecord({ name: 'Daily', fallback_kind: 'line', fallback_line_id: 'line-a' })
  assert.deepEqual(draft, { name: 'Daily', fallback: 'line', line: 'line-a' })
  assert.deepEqual(profileDraftPayload({ ...draft, fallback: 'direct' }), { name: 'Daily', fallback_kind: 'direct' })
  assert.deepEqual(profileDraftPayload({ ...draft, line: 'line-b' }), { name: 'Daily', fallback_kind: 'line', fallback_line_id: 'line-b' })
  assert.throws(() => profileDraftPayload({ ...draft, line: '' }), /线路/)
})

test('editing a routing rule keeps immutable match and validates its new action', () => {
  const draft = ruleDraftFromRecord({ priority: 10, match_type: 'domain', match_value: 'google.com', action: 'line', line_id: 'line-a' })
  assert.deepEqual(draft, { priority: 10, matchType: 'domain', matchValue: 'google.com', action: 'line', line: 'line-a' })
  assert.deepEqual(ruleDraftPayload({ ...draft, priority: 20, action: 'block' }, true), { priority: 20, action: 'block' })
  assert.deepEqual(ruleDraftPayload({ ...draft, line: 'line-b' }, true), { priority: 10, action: 'line', line_id: 'line-b' })
  assert.throws(() => ruleDraftPayload({ ...draft, priority: 0 }, true), /优先级/)
  assert.throws(() => ruleDraftPayload({ ...draft, line: '' }, true), /线路/)
})
