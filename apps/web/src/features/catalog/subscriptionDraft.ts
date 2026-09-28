export type SubscriptionDraft = { name: string; template: string; selected: string[] }

export function subscriptionDraftFromRecord(record: { name: string; name_template: string; proxy_access_ids: string[] }): SubscriptionDraft {
  return { name: record.name, template: record.name_template, selected: [...record.proxy_access_ids] }
}

export function subscriptionDraftPayload(draft: SubscriptionDraft) {
  const name = draft.name.trim()
  if (!name) throw new Error('请输入订阅名称。')
  if (!draft.selected.length) throw new Error('至少选择一个代理连接。')
  if (new Set(draft.selected).size !== draft.selected.length) throw new Error('代理连接不能重复。')
  return { name, name_template: draft.template, proxy_access_ids: [...draft.selected] }
}
