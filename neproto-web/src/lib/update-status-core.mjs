const STATES = new Set([
  "idle",
  "checking",
  "downloading",
  "verifying",
  "extracting",
  "backing_up",
  "installing",
  "restarting",
  "succeeded",
  "failed",
]);

const ACTIVE_STATES = new Set([
  "checking",
  "downloading",
  "verifying",
  "extracting",
  "backing_up",
  "installing",
  "restarting",
]);

const VERSION = /^np2-(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
export const AUTO_UPDATE_CHECK_INTERVAL_MS = 15 * 60 * 1000;
export const UPDATE_CHECK_STALE_MS = 90 * 1000;
export const UPDATE_OPERATION_STALE_MS = 35 * 60 * 1000;
const ALLOWED_KEYS = new Set([
  "schema",
  "state",
  "current_version",
  "available_version",
  "update_available",
  "progress",
  "message",
  "error_code",
  "updated_at",
]);

export function isActiveUpdateState(state) {
  return ACTIVE_STATES.has(state);
}

export function shouldAutomaticallyCheckUpdate({ now, updatedAt, lastRequestedAt, state, checking, polling, visible }) {
  if (!visible || checking || polling || isActiveUpdateState(state) || !Number.isFinite(now)) {
    return false;
  }
  const parsedUpdatedAt = Date.parse(updatedAt);
  const latestKnownCheck = Math.max(
    Number.isFinite(parsedUpdatedAt) ? parsedUpdatedAt : 0,
    Number.isFinite(lastRequestedAt) ? lastRequestedAt : 0,
  );
  return now - latestKnownCheck >= AUTO_UPDATE_CHECK_INTERVAL_MS;
}

export function expireStaleUpdateStatus(status, now) {
  if (!isActiveUpdateState(status.state) || !Number.isFinite(now)) {
    return status;
  }
  const updatedAt = Date.parse(status.updated_at);
  if (!Number.isFinite(updatedAt)) {
    return status;
  }
  const maximumAge = status.state === "checking" ? UPDATE_CHECK_STALE_MS : UPDATE_OPERATION_STALE_MS;
  if (now - updatedAt <= maximumAge) {
    return status;
  }
  const checkTimedOut = status.state === "checking";
  return {
    ...status,
    state: "failed",
    progress: 100,
    message: checkTimedOut ? "Update check timed out" : "Update operation timed out",
    error_code: checkTimedOut ? "update_check_timeout" : "update_operation_timeout",
    updated_at: new Date(now).toISOString(),
  };
}

export function parseUpdateStatus(input) {
  if (typeof input !== "string" || input.length === 0 || input.length > 16 * 1024) {
    throw new Error("invalid update status");
  }
  const value = JSON.parse(input);
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("invalid update status");
  }
  if (Object.keys(value).some((key) => !ALLOWED_KEYS.has(key))) {
    throw new Error("invalid update status");
  }
  if (
    value.schema !== 1 ||
    !STATES.has(value.state) ||
    !VERSION.test(value.current_version) ||
    (value.available_version !== undefined && !VERSION.test(value.available_version)) ||
    typeof value.update_available !== "boolean" ||
    !Number.isInteger(value.progress) ||
    value.progress < 0 ||
    value.progress > 100 ||
    typeof value.message !== "string" ||
    value.message.length > 240 ||
    (value.error_code !== undefined && (typeof value.error_code !== "string" || value.error_code.length > 64)) ||
    typeof value.updated_at !== "string" ||
    !Number.isFinite(Date.parse(value.updated_at))
  ) {
    throw new Error("invalid update status");
  }
  return value;
}
