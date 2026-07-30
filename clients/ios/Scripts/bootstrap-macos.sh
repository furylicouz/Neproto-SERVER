#!/bin/bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly SCRIPT_DIR

"$SCRIPT_DIR/build-frameworks.sh"
"$SCRIPT_DIR/generate-project.sh"
