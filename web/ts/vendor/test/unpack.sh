#!/usr/bin/env bash
# Extracts the committed jsdom install tree (jsdom-node_modules.tar.gz) into
# ./node_modules so the `node --test` frontend tests get a working DOM. jsdom
# can't be bundled into a single file (it reads data files such as its default
# stylesheet from its own package dir at runtime), so its dependency closure is
# vendored — but as ONE deterministic tarball, not ~1800 loose files.
#
# Idempotent: a no-op once node_modules/ exists. Uses only tar/gzip (no package
# manager), so build.sh stays free of npm. Regenerate the tarball via
# web/ts/vendor/rebuild.sh (maintainer-only).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

if [ ! -d node_modules ]; then
  tar -xzf jsdom-node_modules.tar.gz
fi
