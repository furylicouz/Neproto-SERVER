import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const registryUrl = new URL("./registry.ts", import.meta.url);
const globalsUrl = new URL("../../app/globals.css", import.meta.url);

test("production fonts are bundled and never fetched from Google during the build", async () => {
  const [registry, globals] = await Promise.all([
    readFile(registryUrl, "utf8"),
    readFile(globalsUrl, "utf8"),
  ]);
  const fontSources = `${registry}\n${globals}`;

  assert.doesNotMatch(fontSources, /next\/font\/google/);
  assert.doesNotMatch(fontSources, /fonts\.(?:googleapis|gstatic)\.com/);
});
