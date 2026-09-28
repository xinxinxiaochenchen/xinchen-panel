export type RoutingAction = 'direct' | 'block' | 'line'
export type ProfileDraft = { name: string; fallback: RoutingAction; line: string }
export type RuleDraft = { priority: number; matchType: string; matchValue: string; action: RoutingAction; line: string }

export function profileDraftFromRecord(record: { name: string; fallback_kind: string; fallback_line_id: string | null }): ProfileDraft {
  return { name: record.name, fallback: record.fallback_kind as RoutingAction, line: record.fallback_line_id ?? '' }
}

export function profileDraftPayload(draft: ProfileDraft) {
  const name = draft.name.trim()
  if (!name) throw new Error('请输入 Profile 名称。')
  if (draft.fallback === 'line') {
    if (!draft.line) throw new Error('请选择默认线路。')
    return { name, fallback_kind: draft.fallback, fallback_line_id: draft.line }
  }
  return { name, fallback_kind: draft.fallback }
}

export function ruleDraftFromRecord(record: { priority: number; match_type: string; match_value: string; action: string; line_id: string | null }): RuleDraft {
  return { priority: record.priority, matchType: record.match_type, matchValue: record.match_value, action: record.action as RoutingAction, line: record.line_id ?? '' }
}

export function ruleDraftPayload(draft: RuleDraft, editing: boolean) {
  if (!Number.isInteger(draft.priority) || draft.priority < 1 || draft.priority > 1000000) throw new Error('优先级必须是 1 至 1000000 的整数。')
  if (draft.action === 'line' && !draft.line) throw new Error('请选择目标线路。')
  const action = { priority: draft.priority, action: draft.action, ...(draft.action === 'line' ? { line_id: draft.line } : {}) }
  if (editing) return action
  if (!draft.matchValue.trim()) throw new Error('请输入匹配值。')
  return { ...action, match_type: draft.matchType, match_value: draft.matchValue.trim() }
}
