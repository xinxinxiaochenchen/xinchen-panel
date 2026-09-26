export type PasswordChangeResult =
  | { kind: "ready" }
  | { kind: "error"; message: string };

export async function changePassword(
  oldPassword: string,
  newPassword: string,
  csrf: string,
  request: typeof fetch = fetch,
): Promise<PasswordChangeResult> {
  if (newPassword === oldPassword)
    return { kind: "error", message: "新密码不能与当前密码相同。" };
  if (
    new TextEncoder().encode(newPassword).length < 12 ||
    new TextEncoder().encode(newPassword).length > 72
  )
    return { kind: "error", message: "新密码长度需为 12–72 字节。" };
  if (!csrf) return { kind: "error", message: "安全令牌不可用，请刷新页面。" };
  try {
    const response = await request("/api/v1/me/password", {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
      body: JSON.stringify({
        old_password: oldPassword,
        new_password: newPassword,
      }),
    });
    if (response.status === 204) return { kind: "ready" };
    const payload = (await response.json().catch(() => null)) as {
      error?: { message?: string };
    } | null;
    return {
      kind: "error",
      message: payload?.error?.message || `修改失败（${response.status}）。`,
    };
  } catch {
    return { kind: "error", message: "账户服务暂不可用。" };
  }
}
