import type { Snapshot, User } from "./dashboard";

export type ResourceGroup = {
  id: string;
  code: string;
  name: string;
  region: string;
  enabled: boolean;
  created_at: string;
};
export type ForwardTargetPolicy = {
  id: string;
  kind: "public_host" | "node";
  target_group_id: string | null;
  protocol: "TCP" | "UDP";
  port_start: number;
  port_end: number;
  enabled: boolean;
};
export type ForwardPolicyDraft = {
  kind: "public_host" | "node";
  groupID: string;
  protocol: "TCP" | "UDP";
  portStart: number;
  portEnd: number;
};
export type RoleRecord = { id: string; code: string; description: string; system: boolean; permissions: string[]; member_count: number };
export type PermissionRecord = { code: string; description: string };
export type RoleDirectory = { roles: RoleRecord[]; permissions: PermissionRecord[] };
export type AuditRecord = {
  id: string;
  actor_user_id: string;
  actor_email: string;
  action: string;
  object_type: string;
  object_id: string;
  request_id: string;
  created_at: string;
};

export type AdminUsageGroup = "date" | "user" | "node" | "line";
export type AdminUsageFilters = { from: string; to: string; groupBy: AdminUsageGroup; userID?: string; nodeID?: string; lineID?: string };
export type UsageBucket = { date: string; dimension_id: string; uploaded_bytes: number; downloaded_bytes: number; charged_bytes: number };
export type AdminUsageNames = { users: ReadonlyMap<string, string>; nodes: ReadonlyMap<string, string>; lines: ReadonlyMap<string, string> };

export function validateAdminUsageFilters(filters: AdminUsageFilters): string {
  const parse = (value: string): number | null => {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return null;
    const time = Date.parse(`${value}T00:00:00Z`);
    return Number.isFinite(time) && new Date(time).toISOString().slice(0, 10) === value ? time : null;
  };
  const from = parse(filters.from);
  const to = parse(filters.to);
  if (from === null || to === null) return "请输入有效的开始和结束日期。";
  if (to < from) return "结束日期不能早于开始日期。";
  if ((to - from) / 86400000 >= 90) return "统计区间最多为 90 天。";
  return "";
}

export function adminUsageDimensionLabel(groupBy: AdminUsageGroup, id: string, names: AdminUsageNames): string {
  if (groupBy === "date") return "全部资源";
  if (!id) return groupBy === "line" ? "未关联线路" : "未知资源";
  const directory = groupBy === "user" ? names.users : groupBy === "node" ? names.nodes : names.lines;
  return directory.get(id) ?? id;
}

export function buildAdminUsagePath(filters: AdminUsageFilters, cursor?: string | null): string {
  const query = new URLSearchParams({ from: filters.from, to: filters.to, group_by: filters.groupBy });
  if (filters.userID) query.set("user_id", filters.userID);
  if (filters.nodeID) query.set("node_id", filters.nodeID);
  if (filters.lineID) query.set("line_id", filters.lineID);
  query.set("limit", "100");
  if (cursor) query.set("cursor", cursor);
  return `/api/v1/admin/usage/daily?${query.toString()}`;
}

export async function loadAdminUsagePage(filters: AdminUsageFilters, cursor: string | null = null, request: typeof fetch = fetch): Promise<{ kind: "ready"; data: { items: UsageBucket[]; next_cursor: string | null } } | { kind: "empty" } | { kind: "error"; message: string }> {
  try {
    const response = await request(buildAdminUsagePath(filters, cursor), { credentials: "same-origin", cache: "no-store" });
    if (response.status === 404) return { kind: "empty" };
    if (response.status === 401) return { kind: "error", message: "登录已过期，请重新登录。" };
    if (response.status === 403) return { kind: "error", message: "当前账户无权查看流量统计。" };
    if (!response.ok) return { kind: "error", message: `请求失败（${response.status}）` };
    const data = await response.json() as { items?: UsageBucket[]; next_cursor?: string | null };
    if (!Array.isArray(data.items)) return { kind: "error", message: "服务端返回了无效流量统计。" };
    return data.items.length || data.next_cursor ? { kind: "ready", data: { items: data.items, next_cursor: data.next_cursor ?? null } } : { kind: "empty" };
  } catch { return { kind: "error", message: "流量统计暂不可用，请稍后重试。" }; }
}

export function customRolePermissions(permissions: PermissionRecord[]): PermissionRecord[] {
  return permissions.filter((permission) => permission.code !== 'roles.write');
}

export async function loadRoleDirectory(request: typeof fetch = fetch): Promise<{ kind: 'ready'; data: RoleDirectory } | { kind: 'error'; message: string }> {
  try {
    const response = await request('/api/v1/admin/roles', { credentials: 'same-origin', cache: 'no-store' });
    if (!response.ok) return { kind: 'error', message: response.status === 403 ? '当前账户无权查看角色。' : `请求失败（${response.status}）` };
    const data = await response.json() as RoleDirectory;
    if (!Array.isArray(data.roles) || !Array.isArray(data.permissions)) return { kind: 'error', message: '服务端返回了无效角色目录。' };
    return { kind: 'ready', data };
  } catch { return { kind: 'error', message: '角色目录暂不可用，请稍后重试。' }; }
}

export function buildForwardPolicyInput(draft: ForwardPolicyDraft) {
  if (draft.kind === "node" && !draft.groupID) throw new Error("节点目标策略必须选择资源域。");
  if (!Number.isInteger(draft.portStart) || !Number.isInteger(draft.portEnd) || draft.portStart < 1 || draft.portEnd > 65535 || draft.portEnd < draft.portStart) {
    throw new Error("目标端口范围必须在 1–65535 之间，且起始端口不大于结束端口。");
  }
  return {
    kind: draft.kind,
    ...(draft.kind === "node" ? { target_group_id: draft.groupID } : {}),
    protocol: draft.protocol,
    port_start: draft.portStart,
    port_end: draft.portEnd,
    enabled: true,
  };
}
export type Plan = {
  id: string;
  name: string;
  billing_mode?: string;
  period_months?: number;
  quota_bytes: number;
  default_multiplier_milli: number;
  status: string;
  limits: PlanLimits;
  resource_group_ids: string[];
  line_ids: string[];
};
export type Membership = {
  id: string;
  user_id: string;
  plan_id: string;
  starts_at: string;
  ends_at: string;
  status: "scheduled" | "active" | "expired" | "cancelled" | string;
  anchor_day: number;
  timezone: string;
  snapshot: Snapshot;
  created_at: string;
};

export function membershipStatusLabel(status: string, endsAt: string, now = new Date()): string {
  if (status === "active" && new Date(endsAt).getTime() <= now.getTime()) return "已到期";
  return ({ active: "生效中", cancelled: "已取消", expired: "已到期", scheduled: "待生效" } as Record<string, string>)[status] ?? status;
}

export function membershipCanCancel(status: string, endsAt: string, now = new Date()): boolean {
  return (status === "active" || status === "scheduled") && new Date(endsAt).getTime() > now.getTime();
}
export type PlanLimits = {
  max_forward_rules_per_node: number;
  max_subscriptions: number;
  allow_custom_lines: boolean;
  max_custom_lines: number;
  max_hops: number;
  max_proxy_lines: number;
};

export type PlanDraft = {
  name: string;
  quotaGiB: number;
  multiplier: number;
  maxForward: number;
  maxSubscriptions: number;
  maxCustomLines: number;
  maxHops?: number;
  maxProxyLines?: number;
  groupIDs: string[];
  lineIDs: string[];
};

const adminPermissions = [
  "users.read",
  "users.write",
  "roles.read",
  "roles.write",
  "plans.read",
  "plans.write",
  "nodes.write",
  "lines.write",
  "agents.write",
  "forward_policies.write",
  "audit.read",
  "usage.admin",
];

export function hasAdminAccess(permissions: string[]): boolean {
  return adminPermissions.some((permission) =>
    permissions.includes(permission),
  );
}

function wholeNonnegative(value: number, label: string): number {
  if (!Number.isSafeInteger(value) || value < 0)
    throw new Error(`${label}必须是非负整数。`);
  return value;
}

export function buildPlanInput(draft: PlanDraft) {
  const quotaBytes = draft.quotaGiB * 1024 ** 3;
  if (!Number.isSafeInteger(quotaBytes) || quotaBytes < 0)
    throw new Error("流量额度超出允许范围。");
  const multiplierMilli = Math.round(draft.multiplier * 1000);
  if (
    !Number.isSafeInteger(multiplierMilli) ||
    multiplierMilli < 1 ||
    multiplierMilli > 100000
  )
    throw new Error("计费倍率超出允许范围。");
  const maxCustomLines = wholeNonnegative(draft.maxCustomLines, "自建线路数");
  const maxHops = draft.maxHops ?? 1;
  if (!Number.isInteger(maxHops) || maxHops < 1 || maxHops > 8) throw new Error("线路跳数必须是 1 到 8 的整数。");
  const maxProxyLines = draft.maxProxyLines ?? 1;
  if (!Number.isInteger(maxProxyLines) || maxProxyLines < 1 || maxProxyLines > 32) throw new Error("代理候选线路数必须是 1 到 32 的整数。");
  return {
    name: draft.name.trim(),
    quota_bytes: quotaBytes,
    default_multiplier_milli: multiplierMilli,
    limits: {
      max_forward_rules_per_node: wholeNonnegative(
        draft.maxForward,
        "转发规则数",
      ),
      max_subscriptions: wholeNonnegative(draft.maxSubscriptions, "订阅数"),
      allow_custom_lines: maxCustomLines > 0,
      max_custom_lines: maxCustomLines,
      max_hops: maxHops,
      max_proxy_lines: maxProxyLines,
    },
    resource_group_ids: [...draft.groupIDs],
    line_ids: [...draft.lineIDs],
  };
}

export function buildMembershipInput(
  userID: string,
  planID: string,
  starts: string,
  ends: string,
  anchorDay: number,
  timezone: string,
) {
  if (!Number.isInteger(anchorDay) || anchorDay < 1 || anchorDay > 31)
    throw new Error("账期起算日必须在 1–31 之间。");
  const start = new Date(`${starts}T00:00:00Z`);
  const end = new Date(`${ends}T00:00:00Z`);
  if (
    Number.isNaN(start.getTime()) ||
    Number.isNaN(end.getTime()) ||
    end <= start
  )
    throw new Error("套餐日期范围无效。");
  return {
    user_id: userID,
    plan_id: planID,
    starts_at: start.toISOString(),
    ends_at: end.toISOString(),
    anchor_day: anchorDay,
    timezone,
  };
}

export type NodeDraft = {
  groupID: string;
  name: string;
  region: string;
  host: string;
  publicIP?: string;
  bandwidthBPS?: number | null;
  multiplier?: number;
  tags?: string[];
  proxy: boolean;
  forward: boolean;
  proxyPort: number;
  relayPort?: number | null;
};

export function buildNodeInput(draft: NodeDraft) {
  if (!draft.proxy && !draft.forward) throw new Error("节点至少需要一种能力。");
  if (
    draft.proxy &&
    (!Number.isInteger(draft.proxyPort) ||
      draft.proxyPort < 1 ||
      draft.proxyPort > 65535)
  )
    throw new Error("代理端口必须在 1–65535 之间。");
  if (draft.relayPort != null && (!draft.forward || !Number.isInteger(draft.relayPort) || draft.relayPort < 1024 || draft.relayPort > 65535 || (draft.proxy && draft.relayPort === draft.proxyPort)))
    throw new Error("中继端口需要转发能力，且须为不同的 1024–65535 端口。");
  if (draft.bandwidthBPS != null && (!Number.isSafeInteger(draft.bandwidthBPS) || draft.bandwidthBPS < 0))
    throw new Error("带宽必须是非负整数。");
  const multiplier = draft.multiplier ?? 1;
  const multiplierMilli = Math.round(multiplier * 1000);
  if (!Number.isFinite(multiplier) || !Number.isSafeInteger(multiplierMilli) || multiplierMilli < 1 || multiplierMilli > 100000)
    throw new Error("节点倍率必须在 0.001–100 之间。");
  const tags = (draft.tags ?? []).map((tag) => tag.trim());
  if (tags.length > 16 || tags.some((tag) => !tag || tag.length > 32) || new Set(tags).size !== tags.length)
    throw new Error("节点标签必须是最多 16 个不重复的非空标签。");
  return {
    group_id: draft.groupID,
    name: draft.name.trim(),
    region: draft.region.trim(),
    host: draft.host.trim(),
    public_ip: draft.publicIP?.trim() || null,
    bandwidth_bps: draft.bandwidthBPS ?? null,
    multiplier_milli: multiplierMilli,
    tags,
    capabilities: [draft.proxy && "proxy", draft.forward && "forward"].filter(
      (value): value is string => Boolean(value),
    ),
    proxy_port: draft.proxy ? draft.proxyPort : null,
    relay_port: draft.relayPort ?? null,
  };
}

export type AdminUser = User;
