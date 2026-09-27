export function draftLinePayload(name: string, nodeIDs: string[], priority: number, weight: number) {
  if (nodeIDs.length < 2 || nodeIDs.length > 8) throw new Error('请选择 2 至 8 个节点。')
  const ids = nodeIDs.map((id) => id.trim().toLowerCase())
  if (ids.some((id) => !id) || new Set(ids).size !== ids.length) throw new Error('每一跳必须选择不同节点。')
  return {
    name: name.trim(), enabled: false, priority, weight,
    hops: ids.map((node_id, position) => ({
      position, node_id,
      role: position === ids.length - 1 ? 'egress' : position === 0 ? 'ingress' : 'relay',
    })),
  }
}
