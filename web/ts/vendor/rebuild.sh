#!/usr/bin/env bash
# Maintainer-only script. Fetches the pinned upstream sources for the vendored
# browser libraries (Preact, Quill, Lucide) via npm and copies each into
# web/static/vendor/, plus Preact's .d.ts type stubs into web/ts/vendor/preact/.
# It also vendors the test-only jsdom install tree under web/ts/vendor/test/.
#
# Preact and Quill ship prebuilt, self-contained files — Preact's dist/*.module.js
# ESM modules and Quill's dist/quill.js UMD global + dist/quill.snow.css — so no
# bundler is involved: the script only copies (and version-stamps) them. The
# Lucide bundle is the one generated asset here: gen-lucide.mjs (committed,
# fs-only, no network) trims lucide-static's icon set down to the icons the UI
# actually names. Add an icon to its ICONS list before referencing it from a
# component. That keeps the maintainer toolchain to npm + node; build.sh and CI
# need neither.
#
# Every browser filename is version-stamped (e.g. preact-10.29.7.module.js,
# quill-2.0.3.js) from the installed package version, so a file's name records
# exactly which upstream release it came from. The references in
# web/static/index.html (the Preact import map + the Quill <script>/<link> tags)
# are written by hand, so they MUST be updated whenever a version bumps — this
# script prints the current names at the end as a reminder.
#
# NOT invoked by build.sh or CI. Run this by hand only when adding or updating a
# vendored library, then commit the regenerated files.
#
# npm only ever touches a throwaway node_modules here, installed with
# --ignore-scripts (no install-time lifecycle scripts). That keeps the
# package-manager install manual, audited, and out of the automated build.
#
# Requires on $PATH: npm, node.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

VENDOR_DIR="$(pwd)"
BROWSER_OUT="$VENDOR_DIR/../../static/vendor"
PREACT_OUT="$BROWSER_OUT/preact"    # runtime ESM modules served to the browser
QUILL_OUT="$BROWSER_OUT/quill"      # Quill UMD global + snow theme CSS
LUCIDE_OUT="$BROWSER_OUT/lucide"    # generated icon-geometry ESM module
PREACT_TYPES="$VENDOR_DIR/preact"   # .d.ts type stubs (compile-time only)
TEST_DIR="$VENDOR_DIR/test"         # jsdom install tree for the node --test suite

# Read an installed dependency's version from its package.json. Used to stamp the
# vendored filenames so each file's name records its upstream release.
pkgver() { node -p "require('$VENDOR_DIR/node_modules/$1/package.json').version"; }

# Reconcile package-lock.json with package.json first (a no-op producing no diff
# when they're already in sync, so unchanged runs stay deterministic; it adds
# the missing entries after a dependency is added/bumped). Then do a clean,
# lock-pinned install. `npm ci` alone aborts on an out-of-sync lock.
npm install --package-lock-only --ignore-scripts
npm ci --ignore-scripts

PREACT_VER="$(pkgver preact)"
QUILL_VER="$(pkgver quill)"
LUCIDE_VER="$(pkgver lucide-static)"

# --- 1. Preact runtime modules + type stubs --------------------------------
#
# Preact ships prebuilt self-contained ESM (dist/*.module.js) plus its own .d.ts.
# The runtime modules go to web/static/vendor/preact/ (served, version-stamped,
# loaded via the import map); the .d.ts go to web/ts/vendor/preact/ (compile-time
# only, resolved via the tsconfig `paths` entries, so they are NOT version-stamped).

mkdir -p "$PREACT_OUT" "$PREACT_TYPES/src" "$PREACT_TYPES/hooks/src" "$PREACT_TYPES/jsx-runtime/src"

# Filenames are version-stamped, so a bump changes the name rather than
# overwriting. Remove any previously-vendored modules first so old versions
# don't linger in the tree.
rm -f "$PREACT_OUT"/{preact,hooks,jsx-runtime}-*.module.js

PREACT_SRC="$VENDOR_DIR/node_modules/preact"
cp "$PREACT_SRC/dist/preact.module.js"                 "$PREACT_OUT/preact-$PREACT_VER.module.js"
cp "$PREACT_SRC/hooks/dist/hooks.module.js"            "$PREACT_OUT/hooks-$PREACT_VER.module.js"
cp "$PREACT_SRC/jsx-runtime/dist/jsxRuntime.module.js" "$PREACT_OUT/jsx-runtime-$PREACT_VER.module.js"

cp "$PREACT_SRC/src/index.d.ts"             "$PREACT_TYPES/src/index.d.ts"
cp "$PREACT_SRC/src/jsx.d.ts"               "$PREACT_TYPES/src/jsx.d.ts"
cp "$PREACT_SRC/hooks/src/index.d.ts"       "$PREACT_TYPES/hooks/src/index.d.ts"
cp "$PREACT_SRC/jsx-runtime/src/index.d.ts" "$PREACT_TYPES/jsx-runtime/src/index.d.ts"
# dom.d.ts exists only in newer Preact releases (the types were split out of
# jsx.d.ts). Copy it when present so the normalize step below can fix its import.
[ -f "$PREACT_SRC/src/dom.d.ts" ] && cp "$PREACT_SRC/src/dom.d.ts" "$PREACT_TYPES/src/dom.d.ts"

# Preact's .d.ts use extensionless relative imports; this repo's tsconfig uses
# Node16 module resolution, which requires explicit .js extensions. Add them.
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT
cat > "$WORK_DIR/normalize-preact-dts.mjs" <<'EOF'
import { existsSync, readFileSync, writeFileSync } from "node:fs";
const [typesDir] = process.argv.slice(2);
const edits = [
  [`${typesDir}/src/index.d.ts`, [["./jsx", "./jsx.js"], ["./dom", "./dom.js"]]],
  [`${typesDir}/jsx-runtime/src/index.d.ts`, [["../../src/jsx", "../../src/jsx.js"]]],
];
for (const [file, subs] of edits) {
  if (!existsSync(file)) continue;
  let s = readFileSync(file, "utf8");
  for (const [from, to] of subs) s = s.split(`from '${from}'`).join(`from '${to}'`);
  writeFileSync(file, s);
}
EOF
node "$WORK_DIR/normalize-preact-dts.mjs" "$PREACT_TYPES"

echo "Wrote $PREACT_OUT/{preact,hooks,jsx-runtime}-$PREACT_VER.module.js + type stubs"

# --- 2. Quill editor: UMD global + snow theme CSS --------------------------
#
# Quill ships a prebuilt production UMD bundle (dist/quill.js) loaded as a global
# <script> and its snow-theme stylesheet (dist/quill.snow.css). Copy both,
# version-stamped. ComposeForm.tsx declares the Quill global inline, so no .d.ts
# is vendored.

mkdir -p "$QUILL_OUT"
rm -f "$QUILL_OUT"/quill-*.js "$QUILL_OUT"/quill-*.css

QUILL_SRC="$VENDOR_DIR/node_modules/quill"
cp "$QUILL_SRC/dist/quill.js"       "$QUILL_OUT/quill-$QUILL_VER.js"
cp "$QUILL_SRC/dist/quill.snow.css" "$QUILL_OUT/quill-$QUILL_VER.snow.css"

echo "Wrote $QUILL_OUT/quill-$QUILL_VER.js + quill-$QUILL_VER.snow.css"

# --- 3. Lucide: icon geometry for the <Icon> component ---------------------
#
# lucide-static ships the whole collection as data (icon-nodes.json: each icon's
# SVG child elements as [tag, attrs] pairs). MyMail has no icon picker, so
# gen-lucide.mjs emits only the icons its ICONS list names — a few kB rather than
# the ~600 kB full set — as a plain ESM module exporting LUCIDE_ICON_NODES, which
# web/ts/components/Icon.tsx imports as "lucide-icons".

mkdir -p "$LUCIDE_OUT"
rm -f "$LUCIDE_OUT"/lucide-*.js

node "$VENDOR_DIR/gen-lucide.mjs" \
  "$VENDOR_DIR/node_modules/lucide-static/icon-nodes.json" \
  "$LUCIDE_OUT/lucide-$LUCIDE_VER.js" \
  "$LUCIDE_VER"

echo "Wrote $LUCIDE_OUT/lucide-$LUCIDE_VER.js"

# --- 4. Test-only jsdom install tree (never shipped to the browser) ---------
#
# The `node --test` frontend tests need a DOM. jsdom cannot be reduced to a
# single self-contained file — it does dynamic require() of Node builtins and
# reads data files (its default stylesheet, the HTML entity table) from its own
# package dir at runtime — so its dependency closure is vendored instead. It
# gets its own throwaway install under test/, isolated from the browser deps
# above, so nothing but jsdom's own closure ends up in the tarball.

(
  cd "$TEST_DIR"
  npm install --package-lock-only --ignore-scripts
  npm ci --ignore-scripts

  # ONE deterministic tarball rather than ~1800 loose files. --sort/--mtime/
  # --numeric-owner plus `gzip -n` make the archive byte-identical across
  # rebuilds, so an unchanged tree is a no-diff in git. unpack.sh restores it at
  # test time using tar alone, keeping npm out of build.sh and CI.
  tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='UTC 2020-01-01' \
      -cf - node_modules | gzip -n -9 > jsdom-node_modules.tar.gz
  rm -rf node_modules
)

echo "Wrote $TEST_DIR/jsdom-node_modules.tar.gz (jsdom $(node -p "require('$TEST_DIR/package.json').dependencies.jsdom"))"

# --- 5. Reminder: keep web/static/index.html in sync -----------------------
#
# The filenames are version-stamped, so update index.html by hand whenever a
# version bumped: the Preact/Lucide import map and the Quill <script>/<link> tags.
cat <<EOF

Reminder: update web/static/index.html to reference:
  import map: preact             -> ./vendor/preact/preact-$PREACT_VER.module.js
  import map: preact/hooks       -> ./vendor/preact/hooks-$PREACT_VER.module.js
  import map: preact/jsx-runtime -> ./vendor/preact/jsx-runtime-$PREACT_VER.module.js
  import map: lucide-icons       -> ./vendor/lucide/lucide-$LUCIDE_VER.js
  <link ... href="vendor/quill/quill-$QUILL_VER.snow.css">
  <script src="vendor/quill/quill-$QUILL_VER.js"></script>
EOF
