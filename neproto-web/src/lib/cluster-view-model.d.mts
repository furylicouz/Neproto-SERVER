import type { ClusterNode } from "@/lib/admin-api";

export interface ClusterNodeGroup {
  region: string;
  nodes: ClusterNode[];
}

export interface ClusterSummary {
  total: number;
  enabled: number;
  healthy: number;
  clientVisible: number;
}

export function buildClusterGroups(nodes: ClusterNode[], query: string): ClusterNodeGroup[];
export function clusterSummary(nodes: ClusterNode[]): ClusterSummary;
