#!/usr/bin/env bash
# Stop: gate "done" on a passing lint. Exit non-zero blocks completion.
# Uncomment the test line if you want tests enforced on every Stop (slower).
set -euo pipefail
make lint
# make test
