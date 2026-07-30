const HEALTHY_STATES = new Set(["active", "healthy", "ok", "ready", "up"]);
const ISO_COUNTRY_CODES = new Set(
  "AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW".split(
    " ",
  ),
);
const LOCATION_ALIASES = new Map([
  ["moscow", "RU"],
  ["москва", "RU"],
  ["amsterdam", "NL"],
  ["амстердам", "NL"],
  ["helsinki", "FI"],
  ["хельсинки", "FI"],
  ["frankfurt", "DE"],
  ["франкфурт", "DE"],
  ["london", "GB"],
  ["лондон", "GB"],
  ["paris", "FR"],
  ["париж", "FR"],
  ["stockholm", "SE"],
  ["стокгольм", "SE"],
]);

for (const locale of ["en", "ru"]) {
  const names = new Intl.DisplayNames([locale], { type: "region" });
  for (const code of ISO_COUNTRY_CODES) {
    const name = names.of(code);
    if (name) LOCATION_ALIASES.set(normalizeLocation(name), code);
  }
}

export function buildClusterGroups(nodes, query) {
  const needle = String(query || "")
    .trim()
    .toLocaleLowerCase();
  const filtered = nodes.filter((node) => {
    if (!needle) return true;
    const searchable = [
      node.id,
      node.name,
      node.region,
      node.public_identity,
      node.np2_endpoint,
      ...(node.public_addresses || []),
      ...(node.roles || []),
    ]
      .join(" ")
      .toLocaleLowerCase();
    return searchable.includes(needle);
  });

  const groups = new Map();
  for (const node of filtered) {
    const region = String(node.region || "").trim() || "Unassigned";
    const current = groups.get(region) || [];
    current.push(node);
    groups.set(region, current);
  }

  return Array.from(groups, ([region, groupedNodes]) => ({
    region,
    nodes: groupedNodes.toSorted((left, right) => {
      const roleOrder = Number(right.roles.includes("master")) - Number(left.roles.includes("master"));
      return roleOrder || left.name.localeCompare(right.name);
    }),
  })).toSorted((left, right) => {
    const masterOrder =
      Number(right.nodes.some((node) => node.roles.includes("master"))) -
      Number(left.nodes.some((node) => node.roles.includes("master")));
    return masterOrder || left.region.localeCompare(right.region);
  });
}

export function clusterSummary(nodes) {
  return {
    total: nodes.length,
    enabled: nodes.filter((node) => node.enabled).length,
    healthy: nodes.filter((node) => node.enabled && HEALTHY_STATES.has(String(node.health).toLowerCase())).length,
    clientVisible: nodes.filter((node) => node.client_visible).length,
  };
}

export function clusterLocationCode(region) {
  const trimmed = String(region || "").trim();
  if (!trimmed) return null;
  const candidates = [trimmed, ...trimmed.split(/[·/,;|-]/u)].map((value) => value.trim()).filter(Boolean);
  for (const candidate of candidates) {
    const code = candidate.toUpperCase();
    if (ISO_COUNTRY_CODES.has(code)) return code;
    const alias = LOCATION_ALIASES.get(normalizeLocation(candidate));
    if (alias) return alias;
  }
  return null;
}

export function clusterConnectivity(node) {
  const healthy = Boolean(node?.enabled) && HEALTHY_STATES.has(String(node?.health || "").toLowerCase());
  const latency = Number(node?.latency_ms);
  let signal = 0;
  if (healthy && Number.isFinite(latency) && latency > 0) {
    if (latency <= 25) signal = 100;
    else if (latency <= 50) signal = 85;
    else if (latency <= 100) signal = 70;
    else if (latency <= 200) signal = 50;
    else if (latency <= 500) signal = 25;
    else signal = 10;
  }
  return { link: healthy ? 100 : 0, signal, access: node?.client_visible ? 100 : 0 };
}

function normalizeLocation(value) {
  return String(value)
    .normalize("NFKD")
    .replaceAll(/\p{Diacritic}/gu, "")
    .trim()
    .toLocaleLowerCase("en-US");
}
