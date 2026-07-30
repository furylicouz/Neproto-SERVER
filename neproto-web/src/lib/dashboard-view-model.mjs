const HEALTHY_STATES = new Set(["active", "healthy", "ok", "ready", "running", "up"]);

export function buildDashboardSnapshot(overview) {
  const clusterNodes = nonNegative(overview.cluster_nodes);
  const healthyClusterNodes = Math.min(clusterNodes, nonNegative(overview.healthy_cluster_nodes));
  const services = Object.values(overview.services || {});
  return {
    activeUsers: nonNegative(overview.active_users),
    revokedUsers: nonNegative(overview.revoked_users),
    clusterNodes,
    healthyClusterNodes,
    clusterHealthPercent: clusterNodes > 0 ? Math.round((healthyClusterNodes / clusterNodes) * 100) : 0,
    routes: nonNegative(overview.routes),
    enabledRoutes: Math.min(nonNegative(overview.routes), nonNegative(overview.enabled_routes)),
    backups: nonNegative(overview.backups),
    healthyServices: services.filter((state) => HEALTHY_STATES.has(String(state).toLowerCase())).length,
    totalServices: services.length,
    memoryPercent: Math.min(100, nonNegative(overview.host?.memory_percent)),
  };
}

function nonNegative(value) {
  const number = Number(value);
  return Number.isFinite(number) ? Math.max(0, number) : 0;
}
