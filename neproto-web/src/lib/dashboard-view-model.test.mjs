import { buildDashboardSnapshot } from "./dashboard-view-model.mjs";
import assert from "node:assert/strict";
import test from "node:test";

test("dashboard analytics snapshot is derived only from live overview fields", () => {
  const snapshot = buildDashboardSnapshot({
    active_users: 7,
    revoked_users: 2,
    cluster_nodes: 4,
    healthy_cluster_nodes: 3,
    routes: 6,
    enabled_routes: 5,
    backups: 9,
    services: { np2: "active", ingress: "up", web: "failed" },
    host: { memory_percent: 37 },
  });

  assert.deepEqual(snapshot, {
    activeUsers: 7,
    revokedUsers: 2,
    clusterNodes: 4,
    healthyClusterNodes: 3,
    clusterHealthPercent: 75,
    routes: 6,
    enabledRoutes: 5,
    backups: 9,
    healthyServices: 2,
    totalServices: 3,
    memoryPercent: 37,
  });
});

test("dashboard analytics snapshot handles empty and out-of-range counters", () => {
  const snapshot = buildDashboardSnapshot({
    active_users: 0,
    revoked_users: 0,
    cluster_nodes: 0,
    healthy_cluster_nodes: 0,
    routes: 0,
    enabled_routes: 0,
    backups: 0,
    services: { np2: "unknown", ingress: "failed", web: "inactive" },
    host: { memory_percent: 180 },
  });

  assert.equal(snapshot.clusterHealthPercent, 0);
  assert.equal(snapshot.healthyServices, 0);
  assert.equal(snapshot.memoryPercent, 100);
});
