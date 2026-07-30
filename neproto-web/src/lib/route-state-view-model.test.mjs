import assert from "node:assert/strict";
import test from "node:test";

import { normalizeRouteState, routeMatchLabel } from "./route-state-view-model.mjs";

test("route state normalizes null collections returned by an empty Go cluster state", () => {
  const state = normalizeRouteState({
    revision: 1,
    routes: null,
    access: null,
    geodata: { state: "error" },
    schedule: "weekly",
  });

  assert.deepEqual(state.routes, []);
  assert.deepEqual(state.access, []);
  assert.equal(state.geodata.state, "error");
  assert.equal(state.schedule, "weekly");
});

test("route state tolerates legacy incomplete route records without crashing the page", () => {
  const state = normalizeRouteState({
    revision: 7,
    routes: [
      {
        id: "legacy-route",
        name: "Legacy route",
        priority: 100,
        enabled: true,
        source: "admin",
        match: null,
        action: null,
      },
    ],
    access: [{ user_id: "user-1", allowed_node_ids: null, allowed_route_ids: null }],
    geodata: null,
    schedule: null,
  });

  assert.equal(state.routes.length, 1);
  assert.deepEqual(state.routes[0].match, {});
  assert.deepEqual(state.routes[0].action, { kind: "unknown", node_ids: [] });
  assert.deepEqual(state.access[0].allowed_route_ids, []);
  assert.equal(state.geodata.state, "unavailable");
  assert.equal(state.schedule, "off");
  assert.equal(routeMatchLabel(state.routes[0]), "—");
});

test("route match label reports the first populated supported matcher", () => {
  const state = normalizeRouteState({
    routes: [
      {
        id: "openai-nl",
        name: "OpenAI via NL",
        match: { geosite_categories: ["openai"] },
        action: { kind: "node", node_ids: ["edge-nl"] },
      },
    ],
  });

  assert.equal(routeMatchLabel(state.routes[0]), "geosite: openai");
});
