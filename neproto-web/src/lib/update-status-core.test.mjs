import {
  AUTO_UPDATE_CHECK_INTERVAL_MS,
  expireStaleUpdateStatus,
  isActiveUpdateState,
  parseUpdateStatus,
  shouldAutomaticallyCheckUpdate,
} from "./update-status-core.mjs";
import assert from "node:assert/strict";
import test from "node:test";

const status = {
  schema: 1,
  state: "downloading",
  current_version: "np2-0.4.0",
  available_version: "np2-0.4.1",
  update_available: true,
  progress: 15,
  message: "Downloading verified release bundle",
  updated_at: "2026-07-30T00:00:00Z",
};

test("parseUpdateStatus accepts the bounded backend contract", () => {
  assert.deepEqual(parseUpdateStatus(JSON.stringify(status)), status);
});

test("parseUpdateStatus rejects unknown states and out-of-range progress", () => {
  assert.throws(() => parseUpdateStatus(JSON.stringify({ ...status, state: "shell" })));
  assert.throws(() => parseUpdateStatus(JSON.stringify({ ...status, progress: 101 })));
});

test("active state classification matches the updater state machine", () => {
  for (const state of [
    "checking",
    "downloading",
    "verifying",
    "extracting",
    "backing_up",
    "installing",
    "restarting",
  ]) {
    assert.equal(isActiveUpdateState(state), true, state);
  }
  for (const state of ["idle", "succeeded", "failed"]) {
    assert.equal(isActiveUpdateState(state), false, state);
  }
});

test("automatic update checks run for stale visible idle status", () => {
  const now = Date.parse("2026-07-30T12:30:00Z");

  assert.equal(
    shouldAutomaticallyCheckUpdate({
      now,
      updatedAt: new Date(now - AUTO_UPDATE_CHECK_INTERVAL_MS - 1).toISOString(),
      lastRequestedAt: 0,
      state: "idle",
      checking: false,
      polling: false,
      visible: true,
    }),
    true,
  );
});

test("automatic update checks are throttled and pause while hidden or busy", () => {
  const now = Date.parse("2026-07-30T12:30:00Z");
  const stale = new Date(0).toISOString();
  const base = {
    now,
    updatedAt: stale,
    lastRequestedAt: now - 60_000,
    state: "idle",
    checking: false,
    polling: false,
    visible: true,
  };

  assert.equal(shouldAutomaticallyCheckUpdate(base), false, "recent request");
  assert.equal(shouldAutomaticallyCheckUpdate({ ...base, lastRequestedAt: 0, visible: false }), false, "hidden");
  assert.equal(shouldAutomaticallyCheckUpdate({ ...base, lastRequestedAt: 0, checking: true }), false, "checking");
  assert.equal(shouldAutomaticallyCheckUpdate({ ...base, lastRequestedAt: 0, polling: true }), false, "polling");
  assert.equal(
    shouldAutomaticallyCheckUpdate({ ...base, lastRequestedAt: 0, state: "downloading" }),
    false,
    "active update",
  );
});

test("stale active update states terminate instead of polling forever", () => {
  const now = Date.parse("2026-07-30T12:30:00Z");
  const stuckCheck = {
    ...status,
    state: "checking",
    progress: 5,
    updated_at: new Date(now - 90_001).toISOString(),
  };
  const expiredCheck = expireStaleUpdateStatus(stuckCheck, now);
  assert.equal(expiredCheck.state, "failed");
  assert.equal(expiredCheck.error_code, "update_check_timeout");
  assert.equal(expiredCheck.progress, 5);

  const freshInstall = {
    ...status,
    state: "installing",
    progress: 75,
    updated_at: new Date(now - 60_000).toISOString(),
  };
  assert.equal(expireStaleUpdateStatus(freshInstall, now), freshInstall);

  const stuckInstall = {
    ...freshInstall,
    updated_at: new Date(now - 35 * 60_000 - 1).toISOString(),
  };
  const expiredInstall = expireStaleUpdateStatus(stuckInstall, now);
  assert.equal(expiredInstall.state, "failed");
  assert.equal(expiredInstall.error_code, "update_operation_timeout");
  assert.equal(expiredInstall.progress, 75);
});
