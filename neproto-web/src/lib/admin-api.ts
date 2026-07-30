export class AdminAPIError extends Error {
  constructor(
    readonly category: string,
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

export interface ServiceSnapshot {
  np2: string;
  ingress: string;
  web: string;
}

export interface HostSnapshot {
  hostname: string;
  uptime: string;
  load: string;
  memory: string;
  memory_percent: number;
  network_rx: string;
  network_tx: string;
  network_rx_bytes: number;
  network_tx_bytes: number;
}

export interface Overview {
  version: string;
  deployment: string;
  domain: string;
  server_addresses: string[];
  web_enabled: boolean;
  web_domain?: string;
  web_port?: number;
  enable_constellation: boolean;
  enable_forward_secrecy: boolean;
  active_users: number;
  revoked_users: number;
  backups: number;
  cluster_revision: number;
  cluster_nodes: number;
  healthy_cluster_nodes: number;
  routes: number;
  enabled_routes: number;
  geodata_state: string;
  geodata_schedule: string;
  services: ServiceSnapshot;
  host: HostSnapshot;
}

export interface NP2User {
  id: string;
  name: string;
  profile: "quiet" | "web" | "interactive";
  status: "active" | "revoked";
  created_at: string;
  rotated_at?: string;
  revoked_at?: string;
  online: boolean;
  last_seen?: string;
  active_sessions: number;
  online_devices: number;
  enrolled_devices: number;
  max_devices: number;
  upload_bytes: number;
  download_bytes: number;
  total_bytes: number;
  devices: NP2Device[];
}

export interface NP2Device {
  device_id: string;
  online: boolean;
  active_sessions: number;
  first_seen: string;
  last_seen?: string;
}

export interface ClusterNode {
  id: string;
  name: string;
  region: string;
  roles: string[];
  public_identity: string;
  public_addresses: string[];
  np2_endpoint: string;
  enabled: boolean;
  client_visible: boolean;
  provisioned_at: string;
  updated_at: string;
  health: string;
  latency_ms: number;
  checked_at: string;
}

export interface UserAccess {
  user_id: string;
  allowed_node_ids: string[];
  allowed_route_ids: string[];
  allow_auto_selection: boolean;
  allow_client_routes: boolean;
  revision: number;
}

export interface ClusterState {
  cluster_id: string;
  revision: number;
  nodes: ClusterNode[];
  access: UserAccess[];
}

export interface RouteMatch {
  domain_suffixes?: string[];
  cidrs?: string[];
  geoip_countries?: string[];
  geosite_categories?: string[];
  protocols?: string[];
}

export interface ClusterRoute {
  id: string;
  name: string;
  priority: number;
  enabled: boolean;
  source: string;
  mandatory?: boolean;
  match: RouteMatch;
  action: { kind: string; node_ids?: string[] };
}

export interface RouteState {
  revision: number;
  routes: ClusterRoute[];
  access: UserAccess[];
  geodata: { state: string; updated_at?: string; error?: string; geoip_sha256?: string; geosite_sha256?: string };
  schedule: string;
}

export interface ServicesState {
  services: ServiceSnapshot;
}

export interface LogState {
  lines: string[];
}

export interface SettingsState {
  deployment: string;
  domain: string;
  server_addresses: string[];
  web_enabled: boolean;
  web_domain?: string;
  web_port?: number;
  enable_constellation: boolean;
  enable_forward_secrecy: boolean;
}

export interface BackupEntry {
  id: string;
  name: string;
}

export interface BackupState {
  backups: BackupEntry[];
}

export interface GeoDataState {
  status: RouteState["geodata"];
  schedule: string;
}

export interface ControlJob<T = unknown> {
  id: string;
  kind: string;
  state: "queued" | "running" | "succeeded" | "failed";
  progress: number;
  stage: string;
  error?: string;
  result?: T;
  created_at: string;
  started_at?: string;
  finished_at?: string;
}

export async function adminFetch<T>(path: string, init?: RequestInit & { json?: unknown }): Promise<T> {
  const { json, ...requestInit } = init || {};
  const response = await fetch(`/api/admin/${path.replace(/^\/+/, "")}`, {
    cache: "no-store",
    ...requestInit,
    headers: json === undefined ? requestInit.headers : { ...requestInit.headers, "Content-Type": "application/json" },
    body: json === undefined ? requestInit.body : JSON.stringify(json),
  });
  const payload = (await response.json().catch(() => ({ error: "invalid_response" }))) as {
    error?: string;
    message?: string;
  };
  if (!response.ok) {
    throw new AdminAPIError(
      payload.error ?? "request_failed",
      payload.message ?? payload.error ?? "Request failed",
      response.status,
    );
  }
  return payload as T;
}

export async function waitForAdminJob<T>(id: string, onUpdate: (job: ControlJob<T>) => void): Promise<ControlJob<T>> {
  const deadline = Date.now() + 20 * 60 * 1000;
  for (;;) {
    const job = await adminFetch<ControlJob<T>>(`jobs/${encodeURIComponent(id)}`);
    onUpdate(job);
    if (job.state === "succeeded") {
      return job;
    }
    if (job.state === "failed") {
      throw new AdminAPIError("job_failed", job.error || "Operation failed", 500);
    }
    if (Date.now() >= deadline) {
      throw new AdminAPIError("job_timeout", "Operation did not finish in time", 504);
    }
    await new Promise((resolve) => window.setTimeout(resolve, 750));
  }
}
