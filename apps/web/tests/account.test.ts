import test from "node:test";
import assert from "node:assert/strict";
import { changePassword } from "../src/lib/account.ts";

test("password change uses CSRF and accepts empty success response", async () => {
  let sentPath = "";
  let sentBody: unknown;
  let sentToken = "";
  const request = async (input: RequestInfo | URL, init?: RequestInit) => {
    sentPath = String(input);
    sentBody = JSON.parse(String(init?.body));
    sentToken = new Headers(init?.headers).get("X-CSRF-Token") ?? "";
    return new Response(null, { status: 204 });
  };
  const result = await changePassword(
    "old-password",
    "new-password-123",
    "csrf-token",
    request as typeof fetch,
  );
  assert.equal(result.kind, "ready");
  assert.equal(sentPath, "/api/v1/me/password");
  assert.deepEqual(sentBody, {
    old_password: "old-password",
    new_password: "new-password-123",
  });
  assert.equal(sentToken, "csrf-token");
});

test("password change rejects same password without sending a request", async () => {
  const result = await changePassword(
    "same-password",
    "same-password",
    "csrf-token",
    async () => {
      throw new Error("request should not be sent");
    },
  );
  assert.equal(result.kind, "error");
});
