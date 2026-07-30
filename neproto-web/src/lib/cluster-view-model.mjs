const HEALTHY_STATES = new Set(["active", "healthy", "ok", "ready", "up"]);

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
