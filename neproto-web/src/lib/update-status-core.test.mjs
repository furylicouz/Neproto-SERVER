import assert from "node:assert/strict";
import test from "node:test";

import { isActiveUpdateState, parseUpdateStatus } from "./update-status-core.mjs";

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
  for (const state of ["checking", "downloading", "verifying", "extracting", "backing_up", "installing", "restarting"]) {
    assert.equal(isActiveUpdateState(state), true, state);
  }
  for (const state of ["idle", "succeeded", "failed"]) {
    assert.equal(isActiveUpdateState(state), false, state);
  }
});
