#!/usr/bin/env bash
set -Eeuo pipefail

dockerfile=${1:-}
[[ -n $dockerfile && -f $dockerfile ]]

grep -Eq '^RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates( |$)' "$dockerfile"
grep -Fq 'rm -rf /var/lib/apt/lists/*' "$dockerfile"
grep -Fq 'COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt' "$dockerfile"

printf 'PASS: NeProto Docker image installs the system CA trust store\n'
