const segmentPattern = /^[A-Za-z0-9._-]{1,128}$/;
const jobIDPattern = /^[A-Za-z0-9_-]{22}$/;

function matches(method, path, expectedMethod, pattern) {
  if (method !== expectedMethod || path.length !== pattern.length) {
    return false;
  }
  return pattern.every((value, index) => value === "*" || value === path[index]);
}

export function isAllowedAdminControlRequest(method, path) {
  if (
    !Array.isArray(path) ||
    path.length === 0 ||
    path.length > 5 ||
    path.some((segment) => !segmentPattern.test(segment))
  ) {
    return false;
  }
  const normalizedMethod = String(method || "").toUpperCase();
  const staticRoutes = [
    ["GET", ["overview"]],
    ["GET", ["users"]],
    ["POST", ["users"]],
    ["GET", ["cluster"]],
    ["POST", ["cluster", "host-key"]],
    ["POST", ["cluster", "enrol"]],
    ["POST", ["cluster", "sync-users"]],
    ["GET", ["routes"]],
    ["POST", ["routes"]],
    ["GET", ["geodata"]],
    ["POST", ["geodata", "update"]],
    ["POST", ["geodata", "schedule"]],
    ["POST", ["doctor"]],
    ["GET", ["services"]],
    ["POST", ["services", "start"]],
    ["POST", ["services", "stop"]],
    ["POST", ["services", "restart"]],
    ["POST", ["services", "validate"]],
    ["GET", ["logs"]],
    ["GET", ["settings"]],
    ["POST", ["settings", "domain"]],
    ["POST", ["settings", "policy"]],
    ["GET", ["backups"]],
    ["POST", ["backups"]],
    ["POST", ["backups", "restore"]],
  ];
  if (staticRoutes.some(([allowedMethod, pattern]) => matches(normalizedMethod, path, allowedMethod, pattern))) {
    return true;
  }
  if (path[0] === "jobs" && path.length === 2) {
    return normalizedMethod === "GET" && jobIDPattern.test(path[1]);
  }
  const dynamicRoutes = [
    ["GET", ["users", "*", "export"]],
    ["POST", ["users", "*", "rotate"]],
    ["POST", ["users", "*", "revoke"]],
    ["POST", ["users", "*", "cluster-access"]],
    ["PATCH", ["users", "*", "policy"]],
    ["POST", ["users", "*", "traffic-reset"]],
    ["DELETE", ["users", "*", "devices", "*"]],
    ["DELETE", ["users", "*"]],
    ["POST", ["cluster", "nodes", "*", "enable"]],
    ["POST", ["cluster", "nodes", "*", "publish"]],
    ["POST", ["cluster", "nodes", "*", "assign-user"]],
    ["DELETE", ["cluster", "nodes", "*"]],
    ["POST", ["routes", "*", "enable"]],
    ["POST", ["routes", "*", "assign-user"]],
    ["DELETE", ["routes", "*"]],
  ];
  return dynamicRoutes.some(([allowedMethod, pattern]) => matches(normalizedMethod, path, allowedMethod, pattern));
}

export function normalizeAdminControlPath(path) {
  if (!Array.isArray(path) || path.length === 0 || path.some((segment) => !segmentPattern.test(segment))) {
    throw new Error("invalid admin control path");
  }
  return `/v1/${path.map((segment) => encodeURIComponent(segment)).join("/")}`;
}
