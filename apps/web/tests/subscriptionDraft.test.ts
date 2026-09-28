import test from 'node:test'
import assert from 'node:assert/strict'
import { subscriptionDraftFromRecord, subscriptionDraftPayload } from '../src/features/catalog/subscriptionDraft.ts'

test('editing a subscription restores its targets and optional routing profile', () => {
  const draft = subscriptionDraftFromRecord({
    name: 'Laptop', name_template: '{region} · {name}',
    proxy_access_ids: ['access-a', 'access-b'], routing_profile_id: 'profile-a',
  })
  assert.deepEqual(draft, {
    name: 'Laptop', template: '{region} · {name}',
    selected: ['access-a', 'access-b'], profile: 'profile-a',
  })
  assert.deepEqual(subscriptionDraftPayload({ ...draft, profile: '' }), {
    name: 'Laptop', name_template: '{region} · {name}',
    proxy_access_ids: ['access-a', 'access-b'], routing_profile_id: null,
  })
})

test('subscription edits reject empty or duplicate targets before sending a mutation', () => {
  assert.throws(() => subscriptionDraftPayload({ name: 'Laptop', template: '{name}', selected: [], profile: '' }), /至少选择/)
  assert.throws(() => subscriptionDraftPayload({ name: 'Laptop', template: '{name}', selected: ['a', 'a'], profile: '' }), /重复/)
  assert.throws(() => subscriptionDraftPayload({ name: '  ', template: '{name}', selected: ['a'], profile: '' }), /名称/)
})
