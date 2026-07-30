import { buildClusterGroups, clusterConnectivity, clusterLocationCode, clusterSummary } from "./cluster-view-model.mjs";
import assert from "node:assert/strict";
import test from "node:test";

const nodes = [
  {
    id: "edge-nl",
    name: "Netherlands Edge",
    region: "Amsterdam",
    roles: ["ingress", "egress"],
    public_identity: "edge.example.com",
    public_addresses: ["203.0.113.20"],
    np2_endpoint: "edge.example.com:443",
    enabled: true,
    client_visible: true,
    health: "healthy",
    latency_ms: 38,
  },
  {
    id: "master",
    name: "Primary",
    region: "Moscow",
    roles: ["master", "ingress"],
    public_identity: "master.example.com",
    public_addresses: ["203.0.113.10"],
    np2_endpoint: "master.example.com:443",
    enabled: true,
    client_visible: true,
    health: "up",
    latency_ms: 12,
  },
  {
    id: "edge-disabled",
    name: "Reserve",
    region: "",
    roles: ["egress"],
    public_identity: "reserve.example.com",
    public_addresses: ["203.0.113.30"],
    np2_endpoint: "reserve.example.com:443",
    enabled: false,
    client_visible: false,
    health: "down",
    latency_ms: 0,
  },
];

test("cluster nodes are grouped by region with the master region first", () => {
  const groups = buildClusterGroups(nodes, "");

  assert.deepEqual(
    groups.map((group) => [group.region, group.nodes.map((node) => node.id)]),
    [
      ["Moscow", ["master"]],
      ["Amsterdam", ["edge-nl"]],
      ["Unassigned", ["edge-disabled"]],
    ],
  );
});

test("cluster search matches node identity, endpoint, address, role, and region", () => {
  for (const query of ["netherlands", "edge.example", "203.0.113.20", "egress", "amsterdam"]) {
    const groups = buildClusterGroups(nodes, query);
    assert.equal(
      groups.some((group) => group.nodes.some((node) => node.id === "edge-nl")),
      true,
      query,
    );
  }
  assert.deepEqual(buildClusterGroups(nodes, "does-not-exist"), []);
});

test("cluster summary reports real enabled, healthy, and client-visible counts", () => {
  assert.deepEqual(clusterSummary(nodes), {
    total: 3,
    enabled: 2,
    healthy: 2,
    clientVisible: 2,
  });
});

test("cluster location resolves ISO codes, country names, and known cities", () => {
  assert.equal(clusterLocationCode("RU"), "RU");
  assert.equal(clusterLocationCode("Россия · Москва"), "RU");
  assert.equal(clusterLocationCode("Netherlands / Amsterdam"), "NL");
  assert.equal(clusterLocationCode("Unknown rack"), null);
});

test("cluster connectivity meters are derived only from live node state", () => {
  assert.deepEqual(clusterConnectivity(nodes[0]), { link: 100, signal: 85, access: 100 });
  assert.deepEqual(clusterConnectivity(nodes[2]), { link: 0, signal: 0, access: 0 });
});
