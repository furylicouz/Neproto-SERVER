# NP/2 GeoData Operations Contract

## Safety boundary

- Only the authenticated cluster master peer may request an edge update.
- Update metadata is a bounded NP/2 control message; databases are never taken
  from administrator-supplied URLs.
- The updater accepts only fixed V2Fly release endpoints and trusted HTTPS
  redirect hosts.
- GeoIP and GeoSite SHA-256 files are verified before parsing.
- Both protobuf databases must parse successfully before either active matcher
  is replaced.
- A failed download, checksum, parse, activation, or reload keeps the prior
  routing pair active.

## Cluster behavior

1. The master updates and validates its local pair.
2. It sends `GeoDataRequest(version=1, operation=update)` to every configured
   peer through the authenticated NP/2 mux.
3. Each edge downloads, verifies, activates, and hot-reloads independently.
4. The master compares returned GeoIP and GeoSite hashes with its own and
   reports a mismatch as a failed cluster operation.
5. Human and timer invocations share the same code path.

## Automation

`neproto-geodata-update.timer` is persistent and weekly by default. The panel
supports fixed presets only: daily, weekly, monthly, and off. Randomized delay
prevents synchronized downloads. Edge timers update the local edge; a master
timer coordinates all peers.

## Client compatibility

Catalog-v1 clients ignore unknown GeoIP/GeoSite fields, so the catalog retains
the impossible `np2-geodata-never-match.invalid` suffix as a legacy fail-closed
selector. Current clients display the real selectors and hide the sentinel.
Administrator GeoData routes remain server-authoritative.
