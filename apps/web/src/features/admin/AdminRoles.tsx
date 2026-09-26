import { useEffect, useState, type FormEvent } from 'react';
import { ShieldPlus } from 'lucide-react';
import type { User } from '../../lib/dashboard';
import { loadRoleDirectory, type PermissionRecord, type RoleRecord } from '../../lib/admin';
import { mutateCatalog } from '../../lib/catalog';
import { csrfToken } from '../catalog/CreateLine';
import { AdminSection } from './AdminSection';

function PermissionChoices({ available, selected, setSelected }: { available: PermissionRecord[]; selected: string[]; setSelected: (values: string[]) => void }) {
  return <div className="admin-role-permissions">{available.map((permission) => <label className="catalog-check" key={permission.code}>
    <input type="checkbox" checked={selected.includes(permission.code)} onChange={(event) => setSelected(event.target.checked ? [...selected, permission.code] : selected.filter((code) => code !== permission.code))} />
    <span><strong>{permission.code}</strong><small>{permission.description}</small></span>
  </label>)}</div>;
}

export function AdminRoles({ users, canWrite, canAssign, onRefresh }: { users: User[]; canWrite: boolean; canAssign: boolean; onRefresh: () => void }) {
  const [roles, setRoles] = useState<RoleRecord[]>([]);
  const [permissions, setPermissions] = useState<PermissionRecord[]>([]);
  const [generation, setGeneration] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState<RoleRecord | null | undefined>(undefined);
  const [code, setCode] = useState('');
  const [description, setDescription] = useState('');
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [userID, setUserID] = useState('');
  const [userRoles, setUserRoles] = useState<string[]>([]);

  useEffect(() => {
    let active = true;
    setLoading(true);
    void loadRoleDirectory().then((result) => {
      if (!active) return;
      setLoading(false);
      if (result.kind === 'error') { setError(result.message); return; }
      setError(''); setRoles(result.data.roles); setPermissions(result.data.permissions);
    });
    return () => { active = false; };
  }, [generation]);

  function openEditor(role: RoleRecord | null) {
    setEditing(role); setCode(role?.code ?? ''); setDescription(role?.description ?? ''); setSelected(role?.permissions ?? []); setError('');
  }

  async function saveRole(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError('');
    const path = editing ? `/api/v1/admin/roles/${editing.code}` : '/api/v1/admin/roles';
    const body = editing ? { description: description.trim(), permissions: selected } : { code: code.trim(), description: description.trim(), permissions: selected };
    const result = await mutateCatalog<RoleRecord>(path, editing ? 'PATCH' : 'POST', body, csrfToken());
    setBusy(false);
    if (result.kind === 'error') { setError(result.message); return; }
    setEditing(undefined); setGeneration((value) => value + 1);
  }

  async function deleteRole(role: RoleRecord) {
    if (!window.confirm(`删除角色 ${role.code}？`)) return;
    setBusy(true); setError('');
    const result = await mutateCatalog<void>(`/api/v1/admin/roles/${role.code}`, 'DELETE', null, csrfToken());
    setBusy(false);
    if (result.kind === 'error') setError(result.message); else setGeneration((value) => value + 1);
  }

  function chooseUser(id: string) {
    setUserID(id); setUserRoles(users.find((user) => user.id === id)?.roles.filter((role) => role !== 'user' && role !== 'admin') ?? []);
  }

  async function saveUserRoles(event: FormEvent) {
    event.preventDefault(); if (!userID) return;
    setBusy(true); setError('');
    const result = await mutateCatalog<User>(`/api/v1/admin/users/${userID}/roles`, 'PUT', { role_codes: userRoles }, csrfToken());
    setBusy(false);
    if (result.kind === 'error') { setError(result.message); return; }
    setUserID(''); setUserRoles([]); setGeneration((value) => value + 1); onRefresh();
  }

  return <AdminSection title="角色与权限" description="创建自定义角色，并为普通用户追加职责权限；套餐和资源归属限制仍由服务端执行。" action={canWrite ? <button type="button" className="primary-button" onClick={() => openEditor(null)}><ShieldPlus size={16} />创建角色</button> : undefined}>
    {loading && <div className="catalog-state" role="status">正在加载角色…</div>}
    {error && <div className="catalog-state catalog-error" role="alert">{error}</div>}
    {!loading && <div className="admin-table">{roles.map((role) => <div className="admin-row" key={role.code}>
      <strong>{role.code}</strong><span>{role.description}</span><span>{role.permissions.length} 项权限 · {role.member_count} 名用户</span><em>{role.system ? '系统角色' : '自定义'}</em>
      {canWrite && !role.system && <div className="catalog-card-actions"><button className="refresh-button" type="button" disabled={busy} onClick={() => openEditor(role)}>编辑</button><button className="refresh-button" type="button" disabled={busy || role.member_count > 0} onClick={() => void deleteRole(role)}>删除</button></div>}
    </div>)}</div>}
    {canWrite && canAssign && !loading && <form className="admin-role-assignment" onSubmit={(event) => void saveUserRoles(event)}>
      <h3>给普通用户分配角色</h3>
      <select aria-label="选择用户" value={userID} onChange={(event) => chooseUser(event.target.value)}><option value="">选择用户</option>{users.filter((user) => user.roles.includes('user') && !user.roles.includes('admin')).map((user) => <option key={user.id} value={user.id}>{user.email}</option>)}</select>
      {userID && <><PermissionChoices available={roles.filter((role) => !role.system).map((role) => ({ code: role.code, description: role.description }))} selected={userRoles} setSelected={setUserRoles} /><button className="primary-button" type="submit" disabled={busy}>{busy ? '保存中…' : '保存用户角色'}</button></>}
    </form>}
    {editing !== undefined && <div className="catalog-dialog-backdrop" role="presentation"><form className="catalog-dialog" onSubmit={(event) => void saveRole(event)} aria-label={editing ? '编辑角色' : '创建角色'}>
      <div className="catalog-dialog-head"><span className="section-overline">RBAC</span><h2>{editing ? '编辑角色' : '创建角色'}</h2><p>权限变更会在下一次请求立即生效。</p></div>
      <label htmlFor="role-code">角色代码</label><input id="role-code" required disabled={!!editing} maxLength={32} value={code} onChange={(event) => setCode(event.target.value)} />
      <label htmlFor="role-description">说明</label><input id="role-description" required maxLength={200} value={description} onChange={(event) => setDescription(event.target.value)} />
      <label>权限集合</label><PermissionChoices available={permissions} selected={selected} setSelected={setSelected} />
      {error && <div className="auth-error" role="alert">{error}</div>}
      <div className="catalog-form-actions"><button className="refresh-button" type="button" onClick={() => setEditing(undefined)}>取消</button><button className="primary-button" type="submit" disabled={busy}>{busy ? '保存中…' : '保存角色'}</button></div>
    </form></div>}
  </AdminSection>;
}
