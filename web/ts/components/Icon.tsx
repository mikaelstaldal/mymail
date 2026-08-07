import { h } from 'preact';
import { LUCIDE_ICON_NODES } from 'lucide-icons';

interface Props {
  /** Canonical kebab-case Lucide icon name (e.g. "trash-2"). */
  name: string;
  /** Pixel width/height of the square icon. Defaults to 16, not Lucide's 24. */
  size?: number;
  /** Stroke width. Lucide's default is 2. */
  strokeWidth?: number;
  /** Extra class(es), appended after the default `lucide lucide-<name>`. */
  class?: string;
  /** Tooltip text. Also un-hides the icon from assistive tech as a label. */
  title?: string;
}

// Renders a Lucide icon inline as an <svg> whose stroke is `currentColor`, so it
// inherits the surrounding text colour. Returns null for an unknown name — the
// vendored bundle carries only the icons web/ts/vendor/gen-lucide.mjs's ICONS
// list names, so a new icon has to be added there and the bundle regenerated
// (see web/ts/vendor/rebuild.sh) before it can be used here.
export function Icon({ name, size = 16, strokeWidth = 2, class: cls, title }: Props) {
  const nodes = LUCIDE_ICON_NODES[name];
  if (!nodes) return null;
  const className = cls ? `lucide lucide-${name} ${cls}` : `lucide lucide-${name}`;
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width={strokeWidth}
      stroke-linecap="round"
      stroke-linejoin="round"
      class={className}
      role={title ? 'img' : undefined}
      aria-label={title}
      aria-hidden={title ? undefined : 'true'}
    >
      {title && <title>{title}</title>}
      {nodes.map(([tag, attrs], i) => h(tag, { ...attrs, key: i } as never))}
    </svg>
  );
}
