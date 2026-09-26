import { useState, type FormEvent } from "react";
import { Plus } from "lucide-react";
import type { User } from "../../lib/dashboard";
import type { Plan } from "../../lib/admin";
import { buildMembershipInput } from "../../lib/admin";
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
  onSaved,
}: {
  users: User[];
  plans: Plan[];
  onSaved: () => void;
}) {
  const [userID, setUserID] = useState("");
  const [planID, setPlanID] = useState("");
  const [starts, setStarts] = useState(today);
  const [ends, setEnds] = useState(nextYear);
  const [anchorDay, setAnchorDay] = useState(1);
  const [busy, setBusy] = useState(false);
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

  return (
    <AdminSection
      title="套餐授权"
      description="为普通用户分配已有套餐，并设定账期起算日与有效期。"
    >
      <form
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
      </form>
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
