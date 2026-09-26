import { useState, type FormEvent } from "react";
import { Plus, Shield, UserPlus } from "lucide-react";
import type { User } from "../../lib/dashboard";
import { mutateCatalog } from "../../lib/catalog";
import { csrfToken } from "../catalog/CreateLine";
import { AdminSection } from "./AdminSection";

export function UserPanel({
  users,
  canWrite,
  onRefresh,
}: {
  users: User[];
  canWrite: boolean;
  onRefresh: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [statusBusy, setStatusBusy] = useState("");
  const [error, setError] = useState("");
  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    const result = await mutateCatalog<User>(
      "/api/v1/admin/users",
      "POST",
      { email: email.trim(), password, timezone: "Asia/Shanghai" },
      csrfToken(),
    );
    setBusy(false);
    if (result.kind === "error") setError(result.message);
    else {
      setOpen(false);
      setEmail("");
      setPassword("");
      onRefresh();
    }
  }
  async function toggleStatus(item: User) {
    setStatusBusy(item.id);
    setError("");
    const next = item.status === "active" ? "disabled" : "active";
    const result = await mutateCatalog<User>(
      `/api/v1/admin/users/${item.id}`,
      "PATCH",
      { status: next },
      csrfToken(),
    );
    setStatusBusy("");
    if (result.kind === "error") setError(result.message);
    else onRefresh();
  }
  return (
    <AdminSection
      title="用户"
      description="创建普通用户，停用或恢复账户。"
      action={
        canWrite ? (
          <button
            className="primary-button catalog-create-button"
            type="button"
            onClick={() => setOpen(true)}
          >
            <UserPlus size={16} />
            创建用户
          </button>
        ) : undefined
      }
    >
      <div className="admin-table">
        {users.map((item) => (
          <div className="admin-row" key={item.id}>
            <span className="catalog-icon">
              <Shield size={17} />
            </span>
            <strong>{item.email}</strong>
            <span>{item.roles.join(" · ")}</span>
            <em>{item.status === "active" ? "正常" : "已停用"}</em>
            {canWrite && item.roles.includes("user") && !item.roles.includes("admin") && (
              <button className="catalog-toggle admin-status-toggle" type="button" disabled={statusBusy !== ""}
                onClick={() => void toggleStatus(item)}>
                {statusBusy === item.id ? "处理中…" : item.status === "active" ? "停用" : "恢复"}
              </button>
            )}
          </div>
        ))}
        {users.length === 0 && (
          <div className="catalog-state">暂无用户数据。</div>
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
              <span className="section-overline">NEW USER</span>
              <h2>创建普通用户</h2>
              <p>密码只提交给服务端，不会保存在浏览器。</p>
            </div>
            <label htmlFor="admin-user-email">邮箱</label>
            <input
              id="admin-user-email"
              required
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
            <label htmlFor="admin-user-password">初始密码</label>
            <input
              id="admin-user-password"
              required
              minLength={12}
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
            {error && (
              <div className="auth-error" role="alert">
                {error}
              </div>
            )}
            <div className="catalog-form-actions">
              <button
                className="refresh-button"
                type="button"
                onClick={() => { setOpen(false); setPassword(""); setError("") }}
              >
                取消
              </button>
              <button className="primary-button" disabled={busy}>
                {busy ? "创建中…" : "创建用户"}
                <Plus size={16} />
              </button>
            </div>
          </form>
        </div>
      )}
    </AdminSection>
  );
}
