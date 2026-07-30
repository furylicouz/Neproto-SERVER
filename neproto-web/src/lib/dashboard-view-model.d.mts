import type { Overview } from "@/lib/admin-api";

export interface DashboardSnapshot {
  activeUsers: number;
  revokedUsers: number;
  clusterNodes: number;
  healthyClusterNodes: number;
  clusterHealthPercent: number;
  routes: number;
  enabledRoutes: number;
  backups: number;
  healthyServices: number;
  totalServices: number;
  memoryPercent: number;
}

export function buildDashboardSnapshot(overview: Overview): DashboardSnapshot;
