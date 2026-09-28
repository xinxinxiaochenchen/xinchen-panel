export type ForwardDraft = { name: string }

export function forwardDraftFromRecord(record: { name: string }): ForwardDraft {
  return { name: record.name }
}

export function forwardDraftPayload(draft: ForwardDraft): { name: string } {
  const name = draft.name.trim()
  if (!name) throw new Error('请输入规则名称。')
  if ([...name].length > 100) throw new Error('规则名称最多 100 个字符。')
  return { name }
}
