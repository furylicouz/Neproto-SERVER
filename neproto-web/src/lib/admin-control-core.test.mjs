import { isAllowedAdminControlRequest, normalizeAdminControlPath } from "./admin-control-core.mjs";
import assert from "node:assert/strict";
import test from "node:test";

test("admin control allowlist covers product operations and rejects arbitrary paths", () => {
  const allowed = [
    ["GET", ["overview"]],
    ["GET", ["users"]],
    ["GET", ["users", "credential-id", "export"]],
    ["POST", ["users"]],
    ["POST", ["users", "credential-id", "rotate"]],
    ["PATCH", ["users", "credential-id", "policy"]],
    ["POST", ["users", "credential-id", "traffic-reset"]],
    ["DELETE", ["users", "credential-id", "devices", "10223344-5566-7788-99aa-bbccddeef001"]],
    ["DELETE", ["users", "credential-id"]],
    ["POST", ["cluster", "host-key"]],
    ["POST", ["cluster", "enrol"]],
    ["POST", ["cluster", "nodes", "edge-nl", "publish"]],
    ["DELETE", ["cluster", "nodes", "edge-nl"]],
    ["POST", ["routes", "openai", "assign-user"]],
    ["POST", ["geodata", "schedule"]],
    ["POST", ["services", "restart"]],
    ["POST", ["settings", "domain"]],
    ["POST", ["backups", "restore"]],
    ["GET", ["jobs", "0123456789012345678901"]],
  ];
  for (const [method, path] of allowed) {
    assert.equal(isAllowedAdminControlRequest(method, path), true, `${method} ${path.join("/")}`);
  }

  const rejected = [
    ["GET", ["../../etc/passwd"]],
    ["POST", ["services", "shell"]],
    ["DELETE", ["backups", "snapshot"]],
    ["PUT", ["users"]],
    ["GET", ["jobs", "short"]],
    ["GET", ["users", "id", "export", "extra"]],
  ];
  for (const [method, path] of rejected) {
    assert.equal(isAllowedAdminControlRequest(method, path), false, `${method} ${path.join("/")}`);
  }
});

test("control paths are encoded segment by segment", () => {
  assert.equal(normalizeAdminControlPath(["users", "abc_123", "export"]), "/v1/users/abc_123/export");
  assert.throws(() => normalizeAdminControlPath(["users", "../secret"]));
  assert.throws(() => normalizeAdminControlPath([]));
});
