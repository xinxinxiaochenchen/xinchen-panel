import { useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import type { User } from "../../lib/dashboard";
import {
  loadAllCatalogPages,
  type LineRecord,
  type NodeRecord,
} from "../../lib/catalog";
import type { ForwardTargetPolicy, GeoRuleSetRecord, Plan, ResourceGroup } from "../../lib/admin";
import { UserPanel } from "./AdminUsers";
import { PlanPanel } from "./AdminPlans";
import { ResourcePanel } from "./AdminResources";
import { AdminMembershipForm } from "./AdminMembershipForm";
import { AdminForwardPolicies } from "./AdminForwardPolicies";
import { GeoRuleSetPanel } from "./AdminGeoRuleSets";

type AdminData = {
  users: User[];
  plans: Plan[];
  groups: ResourceGroup[];
  nodes: NodeRecord[];
  lines: LineRecord[];
  forwardPolicies: ForwardTargetPolicy[];
  geoRuleSets: GeoRuleSetRecord[];
};
const emptyData: AdminData = {
  users: [],
  plans: [],
  groups: [],
  nodes: [],
  lines: [],
  forwardPolicies: [],
  geoRuleSets: [],
};

export function AdminDirectory({ user }: { user: User }) {
  const [generation, setGeneration] = useState(0);
  const [data, setData] = useState<AdminData>(emptyData);
  const [loading, setLoading] = useState(true);
  const [errors, setErrors] = useState<string[]>([]);
  const [failed, setFailed] = useState<string[]>([]);
  const can = (permission: string) => user.permissions.includes(permission);
  const refresh = () => setGeneration((value) => value + 1);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setErrors([]);
    setFailed([]);
    const requests: { key: keyof AdminData; path: string; allowed: boolean }[] =
      [
        {
          key: "users",
          path: "/api/v1/admin/users",
          allowed: can("users.read"),
        },
        {
          key: "plans",
          path: "/api/v1/admin/plans",
          allowed: can("plans.read"),
        },
        {
          key: "groups",
          path: "/api/v1/admin/resource-groups",
          allowed: can("nodes.write") || can("forward_policies.write"),
        },
        {
          key: "nodes",
          path: "/api/v1/admin/nodes",
          allowed: can("nodes.write") || can("agents.write"),
        },
        {
          key: "lines",
          path: "/api/v1/admin/lines",
          allowed: can("lines.write"),
        },
        {
          key: "forwardPolicies",
          path: "/api/v1/admin/forward-target-policies",
          allowed: can("forward_policies.write"),
        },
        {
          key: "geoRuleSets",
          path: "/api/v1/admin/routing-rule-sets",
          allowed: can("routing_rulesets.read"),
        },
      ];
    void Promise.all(
      requests
        .filter((item) => item.allowed)
        .map(async (request) => ({
          request,
          result: await loadAllCatalogPages<unknown>(request.path),
        })),
    ).then((results) => {
      if (!active) return;
      const next: AdminData = {
        users: [],
        plans: [],
        groups: [],
        nodes: [],
        lines: [],
        forwardPolicies: [],
        geoRuleSets: [],
      };
      const failures: string[] = [];
      const failedKeys: string[] = [];
      for (const { request, result } of results) {
        if (result.kind === "error") {
          failures.push(`${request.path}：${result.message}`);
          failedKeys.push(request.key);
        }
        if (result.kind === "ready")
          (next[request.key] as unknown[]) = result.data;
      }
      setData(next);
      setErrors(failures);
      setFailed(failedKeys);
      setLoading(false);
    });
    return () => {
      active = false;
    };
  }, [user.id, user.permissions, generation]);

  return (
    <div className="catalog-page admin-page">
      <div className="catalog-title">
        <div>
          <span className="section-overline">ADMINISTRATION</span>
          <h1>资源与账户管理</h1>
          <p>配置账户、套餐、资源域、节点和转发目标策略。</p>
        </div>
        <button className="refresh-button" type="button" onClick={refresh}>
          <RefreshCw size={16} />
          刷新数据
        </button>
      </div>
      {loading && (
        <div className="catalog-state" role="status">
          正在加载管理数据…
        </div>
      )}
      {errors.map((error) => (
        <div className="catalog-state catalog-error" role="alert" key={error}>
          {error}
        </div>
      ))}
      {!loading && can("users.read") && !failed.includes("users") && (
        <UserPanel
          users={data.users}
          canWrite={can("users.write")}
          onRefresh={refresh}
        />
      )}
      {!loading && can("plans.read") && !failed.includes("plans") && (
        <PlanPanel
          plans={data.plans}
          groups={data.groups}
          lines={data.lines}
          canWrite={
            can("plans.write") &&
            can("nodes.write") &&
            can("lines.write") &&
            !failed.includes("groups") &&
            !failed.includes("lines")
          }
          onRefresh={refresh}
        />
      )}
      {!loading &&
        can("plans.write") &&
        can("plans.read") &&
        can("users.read") &&
        !failed.includes("users") &&
        !failed.includes("plans") && (
          <AdminMembershipForm
            users={data.users.filter((item) => item.status === "active" && item.roles.includes("user"))}
            plans={data.plans}
            onSaved={refresh}
          />
        )}
      {!loading &&
        (can("nodes.write") || can("agents.write")) &&
        !failed.includes("nodes") && (
          <ResourcePanel
            groups={data.groups}
            nodes={data.nodes}
            canManageNodes={can("nodes.write")}
            canEnrollAgents={can("agents.write")}
            onRefresh={refresh}
          />
        )}
      {!loading && can("forward_policies.write") && !failed.includes("forwardPolicies") && (
        <AdminForwardPolicies policies={data.forwardPolicies} groups={data.groups} onRefresh={refresh} />
      )}
      {!loading && can("routing_rulesets.read") && !failed.includes("geoRuleSets") && (
        <GeoRuleSetPanel sets={data.geoRuleSets} canWrite={can("routing_rulesets.write")} onRefresh={refresh} />
      )}
    </div>
  );
}
