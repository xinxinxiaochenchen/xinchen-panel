import { useState, type FormEvent } from "react";
import { CircleUserRound, KeyRound, ShieldCheck } from "lucide-react";
import type { User } from "../../lib/dashboard";
import { changePassword } from "../../lib/account";
import { csrfToken } from "../catalog/CreateLine";

export function AccountPage({
  user,
  onSessionChanged,
}: {
  user: User;
  onSessionChanged: () => void;
}) {
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    const result = await changePassword(oldPassword, newPassword, csrfToken());
    setBusy(false);
    if (result.kind === "error") setError(result.message);
    else {
      setOldPassword("");
      setNewPassword("");
      onSessionChanged();
    }
  }

  return (
    <div className="catalog-page account-settings">
      <div className="catalog-title">
        <div>
          <span className="section-overline">ACCOUNT & ACCESS</span>
          <h1>账户与权限</h1>
          <p>查看当前会话授权并管理登录密码。</p>
        </div>
      </div>
      <div className="account-settings-grid">
        <section className="dashboard-card">
          <div className="dashboard-card-heading">
            <span>
              <CircleUserRound size={18} />
              账户信息
            </span>
          </div>
          <div className="account-settings-value">
            <small>邮箱</small>
            <strong>{user.email}</strong>
          </div>
          <div className="account-settings-value">
            <small>状态</small>
            <strong>{user.status === "active" ? "正常" : user.status}</strong>
          </div>
          <div className="account-settings-value">
            <small>时区</small>
            <strong>{user.timezone}</strong>
          </div>
        </section>
        <section className="dashboard-card">
          <div className="dashboard-card-heading">
            <span>
              <ShieldCheck size={18} />
              角色与权限
            </span>
          </div>
          <div className="account-settings-value">
            <small>角色</small>
            <strong>{user.roles.join("、") || "无"}</strong>
          </div>
          <div className="account-permissions">
            {user.permissions.map((permission) => (
              <span key={permission}>{permission}</span>
            ))}
          </div>
        </section>
        <section className="dashboard-card account-password-card">
          <div className="dashboard-card-heading">
            <span>
              <KeyRound size={18} />
              修改密码
            </span>
          </div>
          <p>新密码需为 12–72 字节。修改成功后当前会话会退出。</p>
          <form onSubmit={(event) => void submit(event)}>
            <label htmlFor="account-old-password">当前密码</label>
            <input
              id="account-old-password"
              type="password"
              autoComplete="current-password"
              required
              value={oldPassword}
              onChange={(event) => setOldPassword(event.target.value)}
            />
            <label htmlFor="account-new-password">新密码</label>
            <input
              id="account-new-password"
              type="password"
              autoComplete="new-password"
              required
              minLength={12}
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
            />
            {error && (
              <div className="auth-error" role="alert">
                {error}
              </div>
            )}
            <button className="primary-button" type="submit" disabled={busy}>
              {busy ? "正在修改…" : "修改密码"}
            </button>
          </form>
        </section>
      </div>
    </div>
  );
}
