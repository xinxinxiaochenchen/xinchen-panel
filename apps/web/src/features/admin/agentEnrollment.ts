import { mutateCatalog, type MutationResource } from '../../lib/catalog.ts'

export type AgentEnrollmentToken = { token: string; expires_at: string }

export async function issueAgentEnrollmentToken(nodeID: string, password: string, csrf: string, request: typeof fetch = fetch): Promise<MutationResource<AgentEnrollmentToken>> {
  if (!password) return { kind: 'error', message: '请输入当前账户密码。' }
  if (!csrf) return { kind: 'error', message: '安全令牌不可用，请刷新页面后重试。' }
  return mutateCatalog<AgentEnrollmentToken>(`/api/v1/admin/nodes/${encodeURIComponent(nodeID)}/agent-enrollment`, 'POST', { password }, csrf, request)
}
