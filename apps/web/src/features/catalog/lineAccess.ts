export type ToggleLine = { id: string; owner_user_id: string | null }
export type LineActor = { id: string; permissions: string[] }

export function lineTogglePath(line: ToggleLine, actor: LineActor): string | null {
  if (line.owner_user_id === null && actor.permissions.includes('lines.write')) {
    return `/api/v1/admin/lines/${encodeURIComponent(line.id)}`
  }
  if (line.owner_user_id === actor.id && actor.permissions.includes('lines.write.self')) {
    return `/api/v1/lines/${encodeURIComponent(line.id)}`
  }
  return null
}

export const lineEditPath = lineTogglePath

export type LineEditDraft = { name: string; priority: number; weight: number; tags: string; multiplier_milli: number | null }

export function lineEditPayload(draft: LineEditDraft, shared: boolean) {
  const tags = [...new Set(draft.tags.split(',').map((tag) => tag.trim()).filter(Boolean))]
  const common = { name: draft.name.trim(), priority: draft.priority, weight: draft.weight, tags }
  return shared && draft.multiplier_milli !== null ? { ...common, multiplier_milli: draft.multiplier_milli } : common
}
