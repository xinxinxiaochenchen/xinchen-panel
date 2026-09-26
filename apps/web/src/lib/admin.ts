import type { User } from "./dashboard";

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
export type GeoRuleSetRecord = {
  id: string;
  kind: "geosite" | "geoip";
  code: string;
  name: string;
  version: string;
  source: string;
  sha256: string;
  entry_count: number;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};
export type RoleRecord = { id: string; code: string; description: string; system: boolean; permissions: string[]; member_count: number };
export type PermissionRecord = { code: string; description: string };
export type RoleDirectory = { roles: RoleRecord[]; permissions: PermissionRecord[] };

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
  quota_bytes: number;
  default_multiplier_milli: number;
  status: string;
  limits: PlanLimits;
  resource_group_ids: string[];
  line_ids: string[];
};
export type PlanLimits = {
  max_forward_rules_per_node: number;
  max_subscriptions: number;
  max_routing_rules: number;
  allow_custom_lines: boolean;
  max_custom_lines: number;
  max_hops: number;
};

export type PlanDraft = {
  name: string;
  quotaGiB: number;
  multiplier: number;
  maxForward: number;
  maxSubscriptions: number;
  maxRouting: number;
  maxCustomLines: number;
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
  "routing_rulesets.read",
  "routing_rulesets.write",
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
      max_routing_rules: wholeNonnegative(draft.maxRouting, "分流规则数"),
      allow_custom_lines: maxCustomLines > 0,
      max_custom_lines: maxCustomLines,
      max_hops: 1,
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
  proxy: boolean;
  forward: boolean;
  proxyPort: number;
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
  return {
    group_id: draft.groupID,
    name: draft.name.trim(),
    region: draft.region.trim(),
    host: draft.host.trim(),
    capabilities: [draft.proxy && "proxy", draft.forward && "forward"].filter(
      (value): value is string => Boolean(value),
    ),
    proxy_port: draft.proxy ? draft.proxyPort : null,
  };
}

export type AdminUser = User;
