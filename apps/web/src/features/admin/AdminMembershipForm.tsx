import { useState, type FormEvent } from "react";
import { Plus } from "lucide-react";
import type { User } from "../../lib/dashboard";
import { buildMembershipInput, membershipCanCancel, membershipStatusLabel, type Membership, type Plan } from "../../lib/admin";
import { mutateCatalog } from "../../lib/catalog";
import { csrfToken } from "../catalog/CreateLine";
import { AdminSection } from "./AdminSection";

const today = new Date().toISOString().slice(0, 10);
const nextYear = new Date(Date.now() + 365 * 24 * 60 * 60 * 1000)
  .toISOString()
  .slice(0, 10);

export function AdminMembershipForm({
  users,
  plans,
  memberships,
  canWrite,
  onSaved,
}: {
  users: User[];
  plans: Plan[];
  memberships: Membership[];
  canWrite: boolean;
  onSaved: () => void;
}) {
  const [userID, setUserID] = useState("");
  const [planID, setPlanID] = useState("");
  const [starts, setStarts] = useState(today);
  const [ends, setEnds] = useState(nextYear);
  const [anchorDay, setAnchorDay] = useState(1);
  const [busy, setBusy] = useState(false);
  const [cancelBusy, setCancelBusy] = useState("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setSuccess("");
    try {
      const body = buildMembershipInput(
        userID,
        planID,
        starts,
        ends,
        anchorDay,
        "Asia/Shanghai",
      );
      const result = await mutateCatalog<{ id: string }>(
        "/api/v1/admin/memberships",
        "POST",
        body,
        csrfToken(),
      );
      if (result.kind === "error") setError(result.message);
      else {
        setSuccess("套餐授权已创建。");
        onSaved();
      }
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "授权日期无效。");
    } finally {
      setBusy(false);
    }
  }

  async function cancelMembership(membership: Membership) {
    if (!membershipCanCancel(membership.status, membership.ends_at)) return;
    if (!window.confirm(`确认取消 ${membership.snapshot.plan_name} 的授权？现有连接将在配置收敛和额度租约到期后停止。`)) return;
    setCancelBusy(membership.id);
    setError("");
    const result = await mutateCatalog<Membership>(
      `/api/v1/admin/memberships/${membership.id}`,
      "PATCH",
      { status: "cancelled" },
      csrfToken(),
    );
    setCancelBusy("");
    if (result.kind === "error") setError(result.message);
    else onSaved();
  }

  return (
    <AdminSection
      title="套餐授权"
      description="为普通用户分配已有套餐，并设定账期起算日与有效期。"
    >
      <div className="admin-card-grid">
        {memberships.map((membership) => {
          const user = users.find((item) => item.id === membership.user_id);
          return (
            <article className="catalog-card" key={membership.id}>
              <div className="catalog-card-name">
                <strong>{user?.email ?? membership.user_id}</strong>
                <span>{membershipStatusLabel(membership.status, membership.ends_at)}</span>
              </div>
              <div className="catalog-detail-grid">
                <div>
                  <small>套餐</small>
                  <strong>{membership.snapshot.plan_name}</strong>
                </div>
                <div>
                  <small>有效期</small>
                  <strong>{formatMembershipDate(membership.ends_at)}</strong>
                </div>
                <div>
                  <small>账期</small>
                  <strong>{membership.snapshot.period_months ?? 1} 个月 / {membership.anchor_day} 日</strong>
                </div>
                <div>
                  <small>倍率</small>
                  <strong>×{((membership.snapshot.default_multiplier_milli ?? 1000) / 1000).toFixed(3)}</strong>
                </div>
                {canWrite && membershipCanCancel(membership.status, membership.ends_at) && (
                  <button className="catalog-toggle admin-status-toggle" type="button" disabled={cancelBusy !== ""} onClick={() => void cancelMembership(membership)}>
                    {cancelBusy === membership.id ? "取消中…" : "取消授权"}
                  </button>
                )}
              </div>
            </article>
          );
        })}
        {memberships.length === 0 && <div className="catalog-state">暂无授权记录。</div>}
      </div>
      {canWrite && <form
        className="admin-membership-form"
        onSubmit={(event) => void submit(event)}
      >
        <div>
          <label htmlFor="admin-member-user">用户</label>
          <select
            id="admin-member-user"
            required
            value={userID}
            onChange={(event) => setUserID(event.target.value)}
          >
            <option value="">请选择用户</option>
            {users
              .filter(
                (user) =>
                  user.status === "active" && user.roles.includes("user"),
              )
              .map((user) => (
                <option key={user.id} value={user.id}>
                  {user.email}
                </option>
              ))}
          </select>
        </div>
        <div>
          <label htmlFor="admin-member-plan">套餐</label>
          <select
            id="admin-member-plan"
            required
            value={planID}
            onChange={(event) => setPlanID(event.target.value)}
          >
            <option value="">请选择套餐</option>
            {plans
              .filter((plan) => plan.status === "active")
              .map((plan) => (
                <option key={plan.id} value={plan.id}>
                  {plan.name}
                </option>
              ))}
          </select>
        </div>
        <div>
          <label htmlFor="admin-member-start">开始日期（UTC）</label>
          <input
            id="admin-member-start"
            required
            type="date"
            value={starts}
            onChange={(event) => setStarts(event.target.value)}
          />
        </div>
        <div>
          <label htmlFor="admin-member-end">结束日期（UTC）</label>
          <input
            id="admin-member-end"
            required
            type="date"
            value={ends}
            onChange={(event) => setEnds(event.target.value)}
          />
        </div>
        <div>
          <label htmlFor="admin-member-anchor">每月起算日</label>
          <input
            id="admin-member-anchor"
            required
            type="number"
            min={1}
            max={31}
            value={anchorDay}
            onChange={(event) => setAnchorDay(Number(event.target.value))}
          />
        </div>
        <button
          className="primary-button"
          type="submit"
          disabled={busy || !users.length || !plans.length}
        >
          {busy ? "创建中…" : "创建授权"}
          <Plus size={16} />
        </button>
      </form>}
      {error && (
        <p className="catalog-page-error" role="alert">
          {error}
        </p>
      )}
      {success && (
        <p className="catalog-form-note" role="status">
          {success}
        </p>
      )}
    </AdminSection>
  );
}

function formatMembershipDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeZone: "UTC" }).format(date);
}
