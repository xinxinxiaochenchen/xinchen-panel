import test from 'node:test'
import assert from 'node:assert/strict'
import { parseRuleSetFile } from '../src/features/admin/geoRuleSetInput.ts'

test('GeoSite file keeps exact and suffix rules while ignoring comments and blanks', () => {
  assert.deepEqual(parseRuleSetFile('geosite', '# AI services\ndomain:openai.com\n\nsuffix:anthropic.com\n'), ['domain:openai.com', 'suffix:anthropic.com'])
})

test('GeoIP file requires CIDR and rejects oversized sets', () => {
  assert.deepEqual(parseRuleSetFile('geoip', '1.0.1.0/24\n240e::/16'), ['1.0.1.0/24', '240e::/16'])
  assert.throws(() => parseRuleSetFile('geoip', 'example.com'), /CIDR/)
  assert.throws(() => parseRuleSetFile('geosite', '# no entries'), /至少/)
})
