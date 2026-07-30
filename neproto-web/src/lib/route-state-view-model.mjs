const VALID_SCHEDULES = new Set(["off", "daily", "weekly", "monthly"]);

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function stringValue(value, fallback = "") {
  return typeof value === "string" ? value : fallback;
}

function stringList(value) {
  return Array.isArray(value) ? value.filter((item) => typeof item === "string") : [];
}

function normalizeMatch(value) {
  if (!isRecord(value)) return {};
  return {
    domain_suffixes: stringList(value.domain_suffixes),
    cidrs: stringList(value.cidrs),
    geoip_countries: stringList(value.geoip_countries),
    geosite_categories: stringList(value.geosite_categories),
    protocols: stringList(value.protocols),
  };
}

function normalizeRoute(value) {
  if (!isRecord(value)) return null;
  const id = stringValue(value.id).trim();
  if (!id) return null;
  const action = isRecord(value.action) ? value.action : {};
  const priority = Number(value.priority);
  return {
    id,
    name: stringValue(value.name).trim() || id,
    priority: Number.isFinite(priority) ? priority : 0,
    enabled: value.enabled === true,
    source: stringValue(value.source, "admin"),
    mandatory: value.mandatory === true,
    match: normalizeMatch(value.match),
    action: {
      kind: stringValue(action.kind, "unknown") || "unknown",
      node_ids: stringList(action.node_ids),
    },
  };
}

function normalizeAccess(value) {
  if (!isRecord(value)) return null;
  const userID = stringValue(value.user_id).trim();
  if (!userID) return null;
  const revision = Number(value.revision);
  return {
    user_id: userID,
    allowed_node_ids: stringList(value.allowed_node_ids),
    allowed_route_ids: stringList(value.allowed_route_ids),
    allow_auto_selection: value.allow_auto_selection === true,
    allow_client_routes: value.allow_client_routes === true,
    revision: Number.isFinite(revision) && revision >= 0 ? revision : 0,
  };
}

export function normalizeRouteState(value) {
  const state = isRecord(value) ? value : {};
  const geodata = isRecord(state.geodata) ? state.geodata : {};
  const revision = Number(state.revision);
  const schedule = stringValue(state.schedule);
  return {
    revision: Number.isFinite(revision) && revision >= 0 ? revision : 0,
    routes: (Array.isArray(state.routes) ? state.routes : []).map(normalizeRoute).filter(Boolean),
    access: (Array.isArray(state.access) ? state.access : []).map(normalizeAccess).filter(Boolean),
    geodata: {
      state: stringValue(geodata.state, "unavailable") || "unavailable",
      updated_at: stringValue(geodata.updated_at) || undefined,
      error: stringValue(geodata.error) || undefined,
      geoip_sha256: stringValue(geodata.geoip_sha256) || undefined,
      geosite_sha256: stringValue(geodata.geosite_sha256) || undefined,
    },
    schedule: VALID_SCHEDULES.has(schedule) ? schedule : "off",
  };
}

export function routeMatchLabel(route) {
  const match = isRecord(route?.match) ? route.match : {};
  const choices = [
    ["domain", stringList(match.domain_suffixes)],
    ["cidr", stringList(match.cidrs)],
    ["geoip", stringList(match.geoip_countries)],
    ["geosite", stringList(match.geosite_categories)],
    ["protocol", stringList(match.protocols)],
  ];
  const selected = choices.find(([, values]) => values.length > 0);
  return selected ? `${selected[0]}: ${selected[1].join(", ")}` : "—";
}
