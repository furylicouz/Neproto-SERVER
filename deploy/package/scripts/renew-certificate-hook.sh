#!/usr/bin/env bash
set -Eeuo pipefail
exec /usr/local/lib/neproto/sync-certificate --reload
