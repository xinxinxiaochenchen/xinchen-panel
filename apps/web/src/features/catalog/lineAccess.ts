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
