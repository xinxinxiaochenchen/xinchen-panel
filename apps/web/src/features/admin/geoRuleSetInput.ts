export type GeoRuleSetKind = 'geosite' | 'geoip'

export function parseRuleSetFile(kind: GeoRuleSetKind, content: string): string[] {
  if (new TextEncoder().encode(content).length > 6 << 20) throw new Error('规则集文件不能超过 6 MiB。')
  const entries = content.split(/\r?\n/).map((line) => line.trim()).filter((line) => line && !line.startsWith('#'))
  if (entries.length === 0) throw new Error('规则集至少需要一条规则。')
  if (entries.length > 10000) throw new Error('规则集最多包含 10000 条规则。')
  if (kind === 'geoip' && entries.some((entry) => !entry.includes('/'))) throw new Error('GeoIP 文件每一行都必须是 CIDR。')
  return entries
}
