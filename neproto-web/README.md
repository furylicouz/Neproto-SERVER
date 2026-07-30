# NeProto Web

NeProto Web is the Next.js administration frontend shipped inside every
NeProto Server release. It is not deployed as an independent product: the root
release builder creates standalone output and the server installer supervises
it through systemd or Docker.

Local development:

```bash
npm ci --ignore-scripts
npm run dev -- --hostname 0.0.0.0
```

Verification:

```bash
npm run lint -- --max-diagnostics=100
NEPROTO_VERSION=np2-0.5.5 npm run build
```

The production health endpoint is `/api/health`. Deployment, domain, TLS, and
release documentation is maintained in the repository root
[`README.md`](../README.md).

The original dashboard foundation is licensed under the MIT license retained
in this directory.
