import { useState, type FormEvent } from "react";
import { Layers3, Plus } from "lucide-react";
import { mutateCatalog, type LineRecord } from "../../lib/catalog";
import { buildPlanInput, type Plan, type ResourceGroup } from "../../lib/admin";
import { csrfToken } from "../catalog/CreateLine";
import { AdminSection } from "./AdminSection";

export function PlanPanel({
  plans,
  groups,
  lines,
  canWrite,
  canManageStatus,
  onRefresh,
}: {
  plans: Plan[];
  groups: ResourceGroup[];
  lines: LineRecord[];
  canWrite: boolean;
  canManageStatus: boolean;
  onRefresh: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [quota, setQuota] = useState(100);
  const [maxForward, setMaxForward] = useState(5);
  const [maxSubscriptions, setMaxSubscriptions] = useState(3);
  const [maxRouting, setMaxRouting] = useState(20);
  const [maxCustom, setMaxCustom] = useState(0);
  const [maxHops, setMaxHops] = useState(1);
  const [maxProxyLines, setMaxProxyLines] = useState(1);
  const [busy, setBusy] = useState(false);
  const [statusBusy, setStatusBusy] = useState("");
  const [error, setError] = useState("");
  const [multiplier, setMultiplier] = useState(1);
  const [selectedGroups, setSelectedGroups] = useState<string[]>([]);
  const [selectedLines, setSelectedLines] = useState<string[]>([]);
  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const body = buildPlanInput({
        name,
        quotaGiB: quota,
        multiplier,
        maxForward,
        maxSubscriptions,
        maxRouting,
        maxCustomLines: maxCustom,
        maxHops,
        maxProxyLines,
        groupIDs: selectedGroups,
        lineIDs: selectedLines,
      });
      const result = await mutateCatalog<Plan>(
        "/api/v1/admin/plans",
        "POST",
        body,
        csrfToken(),
      );
      if (result.kind === "error") setError(result.message);
      else {
        setOpen(false);
        setName("");
        setSelectedGroups([]);
        setSelectedLines([]);
        onRefresh();
      }
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "表单无效。");
    } finally {
      setBusy(false);
    }
  }
  async function togglePlanStatus(plan: Plan) {
    setStatusBusy(plan.id);
    setError("");
    const result = await mutateCatalog<Plan>(
      `/api/v1/admin/plans/${plan.id}`,
      "PATCH",
      { status: plan.status === "active" ? "archived" : "active" },
      csrfToken(),
    );
    setStatusBusy("");
    if (result.kind === "error") setError(result.message);
    else onRefresh();
  }
  return (
    <AdminSection
      title="套餐"
      description="设置流量额度、规则上限和资源域授权。归档套餐只影响新的授权。"
      action={
        canWrite ? (
          <button
            className="primary-button catalog-create-button"
            type="button"
            onClick={() => setOpen(true)}
          >
            <Layers3 size={16} />
            创建套餐
          </button>
        ) : undefined
      }
    >
      <div className="admin-card-grid">
        {plans.map((item) => (
          <article className="catalog-card" key={item.id}>
            <div className="catalog-card-name">
              <strong>{item.name}</strong>
              <span>{item.status === "active" ? "可授权" : "已归档"}</span>
            </div>
            <div className="catalog-detail-grid">
              <div>
                <small>总流量</small>
                <strong>{(item.quota_bytes / 1024 ** 3).toFixed(0)} GiB</strong>
              </div>
              <div>
                <small>转发上限</small>
                <strong>{item.limits.max_forward_rules_per_node}</strong>
              </div>
              <div>
                <small>订阅上限</small>
                <strong>{item.limits.max_subscriptions}</strong>
              </div>
              <div>
                <small>资源域</small>
                <strong>{item.resource_group_ids.length}</strong>
              </div>
              <div>
                <small>账期</small>
                <strong>{item.period_months ?? 1} 个月按起算日重置</strong>
              </div>
            </div>
            {canManageStatus && (
              <button className="catalog-toggle admin-status-toggle" type="button" disabled={statusBusy !== ""} onClick={() => void togglePlanStatus(item)}>
                {statusBusy === item.id ? "处理中…" : item.status === "active" ? "归档套餐" : "恢复套餐"}
              </button>
            )}
          </article>
        ))}
        {plans.length === 0 && (
          <div className="catalog-state">暂无套餐数据。</div>
        )}
      </div>
      {error && !open && <div className="catalog-state catalog-error" role="alert">{error}</div>}
      {open && (
        <div className="catalog-dialog-backdrop" role="presentation">
          <form
            className="catalog-dialog"
            onSubmit={(event) => void submit(event)}
          >
            <div className="catalog-dialog-head">
              <span className="section-overline">NEW PLAN</span>
              <h2>创建套餐</h2>
              <p>套餐只描述资源额度和授权，不包含支付或订单。</p>
            </div>
            <label htmlFor="admin-plan-name">套餐名称</label>
            <input
              id="admin-plan-name"
              required
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <label htmlFor="admin-plan-quota">总流量（GiB）</label>
            <input
              id="admin-plan-quota"
              required
              type="number"
              min={0}
              value={quota}
              onChange={(event) => setQuota(Number(event.target.value))}
            />
            <div className="catalog-form-row">
              <div>
                <label htmlFor="admin-plan-forward">每节点转发数</label>
                <input
                  id="admin-plan-forward"
                  type="number"
                  min={0}
                  value={maxForward}
                  onChange={(event) =>
                    setMaxForward(Number(event.target.value))
                  }
                />
              </div>
              <div>
                <label htmlFor="admin-plan-subscriptions">订阅数</label>
                <input
                  id="admin-plan-subscriptions"
                  type="number"
                  min={0}
                  value={maxSubscriptions}
                  onChange={(event) =>
                    setMaxSubscriptions(Number(event.target.value))
                  }
                />
              </div>
            </div>
            <label htmlFor="admin-plan-multiplier">默认计费倍率</label>
            <input
              id="admin-plan-multiplier"
              type="number"
              min={0.001}
              max={100}
              step={0.001}
              value={multiplier}
              onChange={(event) => setMultiplier(Number(event.target.value))}
            />
            <label htmlFor="admin-plan-routing">分流规则数</label>
            <input
              id="admin-plan-routing"
              type="number"
              min={0}
              value={maxRouting}
              onChange={(event) => setMaxRouting(Number(event.target.value))}
            />
            <label htmlFor="admin-plan-proxy-lines">每个代理连接的候选线路数</label>
            <input id="admin-plan-proxy-lines" type="number" min={1} max={32} step={1} value={maxProxyLines} onChange={(event) => setMaxProxyLines(Number(event.target.value))} />
            <label htmlFor="admin-plan-custom">自建线路数（0 表示关闭）</label>
            <input
              id="admin-plan-custom"
              type="number"
              min={0}
              value={maxCustom}
              onChange={(event) => setMaxCustom(Number(event.target.value))}
            />
            <span className="catalog-form-label">授权资源域</span>
            <label htmlFor="admin-plan-hops">线路跳数上限</label>
            <input id="admin-plan-hops" type="number" min={1} max={8} step={1} value={maxHops} onChange={(event) => setMaxHops(Number(event.target.value))} />
            <small className="catalog-form-note">代理连接可按套餐上限绑定候选线路，运行时会按优先级和权重选择可用线路。</small>
            <div className="catalog-check-list">
              {groups
                .filter((group) => group.enabled)
                .map((group) => (
                  <label className="catalog-check" key={group.id}>
                    <input
                      type="checkbox"
                      checked={selectedGroups.includes(group.id)}
                      onChange={() =>
                        setSelectedGroups((current) =>
                          current.includes(group.id)
                            ? current.filter((id) => id !== group.id)
                            : [...current, group.id],
                        )
                      }
                    />
                    <span>
                      <strong>{group.code}</strong>
                      <small>
                        {group.name} · {group.region}
                      </small>
                    </span>
                  </label>
                ))}
              {groups.length === 0 && (
                <span className="catalog-form-note">
                  暂无资源域，可稍后新建套餐。
                </span>
              )}
            </div>
            <span className="catalog-form-label">授权共享线路</span>
            <div className="catalog-check-list">
              {lines
                .filter((line) => line.enabled && !line.owner_user_id)
                .map((line) => (
                  <label className="catalog-check" key={line.id}>
                    <input
                      type="checkbox"
                      checked={selectedLines.includes(line.id)}
                      onChange={() =>
                        setSelectedLines((current) =>
                          current.includes(line.id)
                            ? current.filter((id) => id !== line.id)
                            : [...current, line.id],
                        )
                      }
                    />
                    <span>
                      <strong>{line.name}</strong>
                      <small>
                        {line.hops.length} 跳 · 优先级 {line.priority}
                      </small>
                    </span>
                  </label>
                ))}
              {lines.length === 0 && (
                <span className="catalog-form-note">
                  暂无可授权共享线路，可先创建套餐后建立线路。
                </span>
              )}
            </div>
            {error && (
              <div className="auth-error" role="alert">
                {error}
              </div>
            )}
            <div className="catalog-form-actions">
              <button
                className="refresh-button"
                type="button"
                onClick={() => setOpen(false)}
              >
                取消
              </button>
              <button className="primary-button" disabled={busy}>
                {busy ? "创建中…" : "创建套餐"}
                <Plus size={16} />
              </button>
            </div>
          </form>
        </div>
      )}
    </AdminSection>
  );
}
