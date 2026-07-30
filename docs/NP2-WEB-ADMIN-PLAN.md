# NP/2 Web Administration Implementation Plan

- [x] Inventory the interactive `np` console and existing web templates.
- [x] Define the local privilege boundary, API contract, page mapping, and
  dashboard data contract.
- [ ] Make Login v2 the authenticated entry point and remove registration and
  example-auth links from the product navigation.
- [ ] Add failing Go handler tests for overview, users, validation, error
  categories, body limits, and confirmations.
- [ ] Implement the Unix-socket `neprotoctl web-api-server` read model and user
  operations.
- [ ] Add cluster, route, GeoData, service/log, settings, backup, and bounded
  background-job operations with focused tests.
- [ ] Add the authenticated Next.js allowlisted proxy and its tests.
- [ ] Replace fixture dashboard data with the live overview response.
- [ ] Adapt the Users, Infrastructure, Tasks, Analytics, Roles, and Invoice
  templates into the management pages from the contract.
- [ ] Add the control systemd service, Docker socket mount, installer wiring,
  upgrade preservation, and deployment smoke tests.
- [ ] Run repository tests, race tests, vet, web tests/lint/build, installer
  smokes, and browser E2E before release handoff.
