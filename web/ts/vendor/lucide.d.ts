// Types for the generated web/static/vendor/lucide/lucide-<version>.js bundle
// (lucide-static, trimmed to the icons this UI uses — see gen-lucide.mjs).
// Mapped to the bare specifier "lucide-icons" in tsconfig paths + the index.html
// import map, mirroring the other vendored bundles.

/** A single SVG child element of an icon: [tagName, attributes]. */
export type IconNodeChild = [tag: string, attrs: Record<string, string>];

/** One icon's geometry: the ordered child elements that go inside its <svg>. */
export type IconNode = IconNodeChild[];

/** name → icon geometry. Build an <svg> from any entry (see components/Icon). */
export const LUCIDE_ICON_NODES: Record<string, IconNode>;
