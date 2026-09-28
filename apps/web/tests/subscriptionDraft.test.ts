import test from 'node:test'
import assert from 'node:assert/strict'
import { subscriptionDraftFromRecord, subscriptionDraftPayload } from '../src/features/catalog/subscriptionDraft.ts'

test('editing a subscription restores targets and sends only supported fields', () => {
  const draft = subscriptionDraftFromRecord({
    name: 'Laptop', name_template: '{region} · {name}',
    proxy_access_ids: ['access-a', 'access-b'],
  })
  assert.deepEqual(draft, {
    name: 'Laptop', template: '{region} · {name}',
    selected: ['access-a', 'access-b'],
  })
  assert.deepEqual(subscriptionDraftPayload(draft), {
    name: 'Laptop', name_template: '{region} · {name}',
    proxy_access_ids: ['access-a', 'access-b'],
  })
})

test('subscription edits reject empty or duplicate targets before sending a mutation', () => {
  assert.throws(() => subscriptionDraftPayload({ name: 'Laptop', template: '{name}', selected: [] }), /至少选择/)
  assert.throws(() => subscriptionDraftPayload({ name: 'Laptop', template: '{name}', selected: ['a', 'a'] }), /重复/)
  assert.throws(() => subscriptionDraftPayload({ name: '  ', template: '{name}', selected: ['a'] }), /名称/)
})
