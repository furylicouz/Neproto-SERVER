import assert from "node:assert/strict";
import test from "node:test";

import { createAdminSession, secureEqual, verifyAdminSession } from "./admin-session-core.mjs";

test("admin session verifies before expiry", () => {
  const token = createAdminSession("a".repeat(64), 1_000, 3_600, Buffer.alloc(16, 7));
  assert.equal(verifyAdminSession(token, "a".repeat(64), 2_000), true);
});

test("admin session rejects tampering, another secret, and expiry", () => {
  const token = createAdminSession("a".repeat(64), 1_000, 3_600, Buffer.alloc(16, 7));
  assert.equal(verifyAdminSession(`${token}x`, "a".repeat(64), 2_000), false);
  assert.equal(verifyAdminSession(token, "b".repeat(64), 2_000), false);
  assert.equal(verifyAdminSession(token, "a".repeat(64), 4_601), false);
});

test("secureEqual handles unequal lengths without throwing", () => {
  assert.equal(secureEqual("secret", "secret"), true);
  assert.equal(secureEqual("secret", "short"), false);
});
