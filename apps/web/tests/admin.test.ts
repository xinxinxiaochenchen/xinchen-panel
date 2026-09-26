import test from "node:test";
import assert from "node:assert/strict";
import {
  buildPlanInput,
  buildMembershipInput,
  buildNodeInput,
  hasAdminAccess,
} from "../src/lib/admin.ts";

test("plan input converts GiB quota to integer bytes and preserves grants", () => {
  const input = buildPlanInput({
    name: "Standard",
    quotaGiB: 100,
    multiplier: 1.5,
    maxForward: 4,
    maxSubscriptions: 3,
    maxRouting: 20,
    maxCustomLines: 2,
    groupIDs: ["group-id"],
    lineIDs: ["line-id"],
  });
  assert.equal(input.quota_bytes, 107374182400);
  assert.equal(input.default_multiplier_milli, 1500);
  assert.deepEqual(input.resource_group_ids, ["group-id"]);
  assert.equal(input.limits.allow_custom_lines, true);
  assert.equal(input.limits.max_hops, 1);
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
  assert.equal(hasAdminAccess(["dashboard.read", "nodes.read"]), false);
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
