export type ProxyLineDraft = { lineID: string; priority: number; weight: number }

export type ProxyAccessDraftRecord = {
  line_id: string
  line_ids?: string[]
  line_options?: { line_id: string; priority: number; weight: number }[]
}

export function proxyAccessDraftFromRecord(access: ProxyAccessDraftRecord): ProxyLineDraft[] {
  const ids = access.line_ids?.length ? access.line_ids : [access.line_id]
  return ids.map((lineID) => {
    const option = access.line_options?.find((item) => item.line_id === lineID)
    return { lineID, priority: option?.priority ?? 100, weight: option?.weight ?? 1 }
  })
}

export function proxyAccessPayload(name: string, lines: ProxyLineDraft[]) {
  if (!lines.length || lines.some((line) => !line.lineID)) throw new Error('至少选择一条线路。')
  if (lines.length > 32) throw new Error('最多选择 32 条线路。')
  if (new Set(lines.map((line) => line.lineID)).size !== lines.length) throw new Error('线路不能重复。')
  if (lines.some((line) => !Number.isInteger(line.priority) || line.priority < 0 || line.priority > 1000)) throw new Error('优先级须在 0 至 1000 之间。')
  if (lines.some((line) => !Number.isInteger(line.weight) || line.weight < 1 || line.weight > 100)) throw new Error('权重须在 1 至 100 之间。')
  return {
    name: name.trim(),
    line_id: lines[0].lineID,
    line_ids: lines.map((line) => line.lineID),
    line_options: lines.map((line) => ({ line_id: line.lineID, priority: line.priority, weight: line.weight })),
  }
}
