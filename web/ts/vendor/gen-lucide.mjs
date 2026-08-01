// Maintainer-only generator, invoked by rebuild.sh. Reads lucide-static's
// icon-nodes.json (every icon's SVG child elements as [tag, attrs] pairs) and
// emits web/static/vendor/lucide/lucide-<version>.js — a plain ESM module
// exporting LUCIDE_ICON_NODES for <Icon> (web/ts/components/Icon.tsx) to build
// an <svg> from.
//
// MyMail only ever renders the fixed set the UI names, so the bundle carries
// exactly ICONS and nothing else (a few kB instead of ~600 kB). Adding an icon
// to the UI therefore means: add its name here, re-run rebuild.sh, and commit
// the regenerated bundle. <Icon> renders nothing for a name that is not in the
// bundle.
//
// fs-only, no network.

import { readFileSync, writeFileSync } from 'node:fs';

// Every Lucide icon the web UI references, by canonical kebab-case name.
// Keep sorted; keep in sync with the `name=` props across web/ts/.
const ICONS = [
  'alarm-clock',
  'arrow-left',
  'arrow-right',
  'calendar-plus',
  'chevron-down',
  'chevron-left',
  'chevron-right',
  'clock',
  'file-pen',
  'folder',
  'grip-vertical',
  'inbox',
  'mail',
  'moon',
  'paperclip',
  'pencil',
  'refresh-cw',
  'search',
  'send',
  'settings',
  'sun',
  'trash-2',
  'triangle-alert',
  'x',
];

const [nodesPath, outPath, version] = process.argv.slice(2);
if (!nodesPath || !outPath || !version) {
  console.error('usage: gen-lucide.mjs <icon-nodes.json> <out.js> <version>');
  process.exit(1);
}

const all = JSON.parse(readFileSync(nodesPath, 'utf8'));

const picked = {};
for (const name of ICONS) {
  const nodes = all[name];
  if (!nodes) {
    console.error(`gen-lucide.mjs: lucide-static ${version} has no icon "${name}"`);
    process.exit(1);
  }
  picked[name] = nodes;
}

// One entry per line: the bundle is committed, so a readable diff matters more
// than the handful of bytes minifying would save.
const body = Object.entries(picked)
  .map(([name, nodes]) => `  ${JSON.stringify(name)}: ${JSON.stringify(nodes)},`)
  .join('\n');

writeFileSync(
  outPath,
  `// AUTO-GENERATED — do not edit. Regenerate via web/ts/vendor/rebuild.sh.\n` +
  `// Source: lucide-static ${version} (ISC License). ${ICONS.length} icons,\n` +
  `// selected by the ICONS list in web/ts/vendor/gen-lucide.mjs.\n` +
  `export const LUCIDE_ICON_NODES = {\n${body}\n};\n`,
);
