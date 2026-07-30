import type { ClusterRoute, RouteState } from "@/lib/admin-api";

export function normalizeRouteState(value: unknown): RouteState;
export function routeMatchLabel(route: ClusterRoute): string;
