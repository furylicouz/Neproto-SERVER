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
