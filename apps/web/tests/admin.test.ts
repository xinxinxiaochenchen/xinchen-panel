import test from "node:test";
import assert from "node:assert/strict";
import {
  buildPlanInput,
  buildMembershipInput,
  buildNodeInput,
  buildForwardPolicyInput,
  hasAdminAccess,
  buildAdminUsagePath,
  loadAdminUsagePage,
  validateAdminUsageFilters,
  adminUsageDimensionLabel,
  membershipStatusLabel,
  membershipCanCancel,
} from "../src/lib/admin.ts";

test("membership status reflects expiry even before the database cleanup", () => {
  const now = new Date("2026-09-28T12:00:00Z");
  assert.equal(membershipStatusLabel("active", "2026-09-28T11:59:59Z", now), "已到期");
  assert.equal(membershipStatusLabel("active", "2026-09-29T00:00:00Z", now), "生效中");
  assert.equal(membershipStatusLabel("cancelled", "2026-09-29T00:00:00Z", now), "已取消");
  assert.equal(membershipCanCancel("active", "2026-09-29T00:00:00Z", now), true);
  assert.equal(membershipCanCancel("active", "2026-09-28T11:59:59Z", now), false);
  assert.equal(membershipCanCancel("cancelled", "2026-09-29T00:00:00Z", now), false);
});

test("plan input converts GiB quota to integer bytes and preserves grants", () => {
  const input = buildPlanInput({
    name: "Standard",
    quotaGiB: 100,
    multiplier: 1.5,
    maxForward: 4,
    maxSubscriptions: 3,
    maxCustomLines: 2,
    maxHops: 3,
    maxProxyLines: 2,
    groupIDs: ["group-id"],
    lineIDs: ["line-id"],
  });
  assert.equal(input.quota_bytes, 107374182400);
  assert.equal(input.default_multiplier_milli, 1500);
  assert.deepEqual(input.resource_group_ids, ["group-id"]);
  assert.equal(input.limits.allow_custom_lines, true);
  assert.equal(input.limits.max_hops, 3);
  assert.equal(input.limits.max_proxy_lines, 2);
  assert.throws(() => buildPlanInput({ name: "Bad", quotaGiB: 1, multiplier: 1, maxForward: 0, maxSubscriptions: 0, maxCustomLines: 0, maxHops: 9, groupIDs: [], lineIDs: [] }));
  assert.throws(() => buildPlanInput({ name: "Bad", quotaGiB: 1, multiplier: 1, maxForward: 0, maxSubscriptions: 0, maxCustomLines: 0, maxProxyLines: 33, groupIDs: [], lineIDs: [] }));
});

test("membership dates use UTC instants and a bounded anchor day", () => {
  const input = buildMembershipInput(
    "user-id",
    "plan-id",
    "2026-09-26",
    "2026-10-26",
    26,
    "Asia/Shanghai",
  );
  assert.equal(input.starts_at, "2026-09-26T00:00:00.000Z");
  assert.equal(input.ends_at, "2026-10-26T00:00:00.000Z");
  assert.equal(input.anchor_day, 26);
  assert.throws(() =>
    buildMembershipInput("u", "p", "2026-09-26", "2026-10-26", 32, "UTC"),
  );
});

test("administrator access follows permissions instead of role label", () => {
  assert.equal(hasAdminAccess(["nodes.write"]), true);
  assert.equal(hasAdminAccess(["audit.read"]), true);
  assert.equal(hasAdminAccess(["dashboard.read", "nodes.read"]), false);
});

test("admin usage path preserves filters and opaque cursors", () => {
  assert.equal(buildAdminUsagePath({ from: "2026-09-01", to: "2026-09-14", groupBy: "line", userID: "user-1", lineID: "line-1" }, "a/b+=="), "/api/v1/admin/usage/daily?from=2026-09-01&to=2026-09-14&group_by=line&user_id=user-1&line_id=line-1&limit=100&cursor=a%2Fb%2B%3D%3D");
  assert.equal(buildAdminUsagePath({ from: "2026-09-01", to: "2026-09-14", groupBy: "date" }), "/api/v1/admin/usage/daily?from=2026-09-01&to=2026-09-14&group_by=date&limit=100");
});

test("admin usage date range accepts at most 90 inclusive UTC days", () => {
  assert.equal(validateAdminUsageFilters({ from: "2026-07-01", to: "2026-09-28", groupBy: "date" }), "");
  assert.match(validateAdminUsageFilters({ from: "2026-07-01", to: "2026-09-29", groupBy: "date" }), /90/);
  assert.match(validateAdminUsageFilters({ from: "2026-09-28", to: "2026-09-27", groupBy: "date" }), /结束/);
  assert.match(validateAdminUsageFilters({ from: "2026-02-30", to: "2026-03-01", groupBy: "date" }), /日期/);
});

test("admin usage loader preserves paged buckets and reports invalid responses", async () => {
  const filters = { from: "2026-09-01", to: "2026-09-14", groupBy: "node" as const };
  const bucket = { date: "2026-09-14", dimension_id: "node-1", uploaded_bytes: 10, downloaded_bytes: 20, charged_bytes: 45 };
  assert.deepEqual(await loadAdminUsagePage(filters, "next", async (path) => {
    assert.match(String(path), /cursor=next/);
    return Response.json({ items: [bucket], next_cursor: "later" });
  }), { kind: "ready", data: { items: [bucket], next_cursor: "later" } });
  assert.deepEqual(await loadAdminUsagePage(filters, null, async () => Response.json({ items: null, next_cursor: null })), { kind: "error", message: "服务端返回了无效流量统计。" });
});

test("admin usage dimension labels distinguish unknown and unattributed resources", () => {
  const names = { users: new Map([["u1", "a@example.test"]]), nodes: new Map([["n1", "JP"]]), lines: new Map([["l1", "JP via HK"]]) };
  assert.equal(adminUsageDimensionLabel("date", "", names), "全部资源");
  assert.equal(adminUsageDimensionLabel("user", "u1", names), "a@example.test");
  assert.equal(adminUsageDimensionLabel("node", "n1", names), "JP");
  assert.equal(adminUsageDimensionLabel("line", "l1", names), "JP via HK");
  assert.equal(adminUsageDimensionLabel("line", "", names), "未关联线路");
  assert.equal(adminUsageDimensionLabel("node", "missing", names), "missing");
});

test("proxy nodes require a valid proxy port and forward-only nodes omit it", () => {
  assert.throws(() =>
    buildNodeInput({
      groupID: "group",
      name: "JP",
      region: "Japan",
      host: "jp.example.test",
      proxy: true,
      forward: false,
      proxyPort: 0,
    }),
  );
  const node = buildNodeInput({
    groupID: "group",
    name: "HK",
    region: "Hong Kong",
    host: "hk.example.test",
    proxy: false,
    forward: true,
    proxyPort: 0,
  });
  assert.deepEqual(node.capabilities, ["forward"]);
  assert.equal(node.proxy_port, null);
});

test("relay listener port requires forward capability and cannot reuse proxy port", () => {
  const draft = { groupID: "group", name: "Relay", region: "JP", host: "relay.example.com", proxy: true, forward: true, proxyPort: 443, relayPort: 24443 };
  assert.equal(buildNodeInput(draft).relay_port, 24443);
  assert.throws(() => buildNodeInput({ ...draft, relayPort: 443 }), /中继端口/);
  assert.throws(() => buildNodeInput({ ...draft, relayPort: 1023 }), /中继端口/);
  assert.throws(() => buildNodeInput({ ...draft, forward: false }), /中继端口/);
});

test("node input preserves public address bandwidth multiplier and tags", () => {
  const node = buildNodeInput({
    groupID: "group",
    name: "Tokyo",
    region: "JP",
    host: "tokyo.example.test",
    publicIP: "203.0.113.10",
    bandwidthBPS: 1_000_000_000,
    multiplier: 1.25,
    tags: ["premium", " jp1 "],
    proxy: true,
    forward: true,
    proxyPort: 443,
    relayPort: 24443,
  });
  assert.equal(node.public_ip, "203.0.113.10");
  assert.equal(node.bandwidth_bps, 1_000_000_000);
  assert.equal(node.multiplier_milli, 1250);
  assert.deepEqual(node.tags, ["premium", "jp1"]);
});

test("forward destination policy requires a bounded port range and node group", () => {
  assert.deepEqual(buildForwardPolicyInput({ kind: "node", groupID: "group-id", protocol: "TCP", portStart: 443, portEnd: 444 }), {
    kind: "node", target_group_id: "group-id", protocol: "TCP", port_start: 443, port_end: 444, enabled: true,
  });
  assert.deepEqual(buildForwardPolicyInput({ kind: "public_host", groupID: "ignored", protocol: "UDP", portStart: 53, portEnd: 53 }), {
    kind: "public_host", protocol: "UDP", port_start: 53, port_end: 53, enabled: true,
  });
  assert.throws(() => buildForwardPolicyInput({ kind: "node", groupID: "", protocol: "TCP", portStart: 443, portEnd: 443 }));
  assert.throws(() => buildForwardPolicyInput({ kind: "public_host", groupID: "", protocol: "TCP", portStart: 500, portEnd: 499 }));
  assert.throws(() => buildForwardPolicyInput({ kind: "public_host", groupID: "", protocol: "TCP", portStart: 0, portEnd: 1 }));
});
