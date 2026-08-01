import { useRef, useEffect } from 'preact/hooks';
import { formatDateAdaptive } from '../util/date.js';
import { Icon } from './Icon.js';
import type { components } from '../api/types.js';

type MessageSummary = components['schemas']['MessageSummary'];
type Folder = components['schemas']['Folder'];

export interface MessageListProps {
  items: MessageSummary[];
  selectedIds: Set<number>;
  onToggleSelect: (id: number) => void;
  onToggleSelectAll: () => void;
  onRowClick: (id: number) => void;
  snippets?: Record<number, string>;
  folders?: Folder[];
}

function renderSnippet(text: string) {
  const out: preact.JSX.Element[] = [];
  let last = 0;
  let key = 0;
  const re = /\*\*(.+?)\*\*/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push(<span key={key++}>{text.slice(last, m.index)}</span>);
    out.push(<mark key={key++} class="snippet-mark">{m[1]}</mark>);
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push(<span key={key++}>{text.slice(last)}</span>);
  return out;
}

function IndeterminateCheckbox({ checked, indeterminate, onChange, ariaLabel }: {
  checked: boolean;
  indeterminate: boolean;
  onChange: () => void;
  ariaLabel: string;
}) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = indeterminate;
  }, [indeterminate]);
  return (
    <input
      ref={ref}
      type="checkbox"
      checked={checked}
      onChange={onChange}
      aria-label={ariaLabel}
    />
  );
}


function senderName(from: string): string {
  const m = from.match(/^([^<>]+?)\s*<[^>]+>$/);
  return m ? m[1].trim() : from;
}

function Badges({ msg }: { msg: MessageSummary }) {
  return (
    <>
      {msg.has_attachments && (
        <Icon name="paperclip" size={14} class="msg-badge" title="Has attachments" />
      )}
      {msg.send_failed && msg.folder_id !== 4 && (
        <span class="msg-badge msg-badge-fail" title="Send failed">!</span>
      )}
    </>
  );
}

export function MessageList({
  items,
  selectedIds,
  onToggleSelect,
  onToggleSelectAll,
  onRowClick,
  snippets,
  folders,
}: MessageListProps) {
  const allSelected = items.length > 0 && items.every(m => selectedIds.has(m.id));
  const someSelected = selectedIds.size > 0;
  const folderMap = folders ? new Map(folders.map(f => [f.id, f.name])) : null;

  return (
    <table class={`msg-table${snippets ? ' has-snippets' : ''}`}>
      <thead>
        <tr>
          <th class="col-check">
            <IndeterminateCheckbox
              checked={allSelected}
              indeterminate={someSelected && !allSelected}
              onChange={onToggleSelectAll}
              ariaLabel="Select all messages"
            />
          </th>
          <th class="col-from">From</th>
          <th class="col-subject">Subject</th>
          {folderMap && <th class="col-folder">Folder</th>}
          <th class="col-date">Date</th>
        </tr>
      </thead>
      <tbody>
        {items.map(msg => {
          const { display, title } = formatDateAdaptive(msg.date);
          const selected = selectedIds.has(msg.id);
          return (
            <tr
              key={msg.id}
              class={`msg-row${msg.read ? '' : ' unread'}${selected ? ' selected' : ''}`}
              onClick={() => onRowClick(msg.id)}
            >
              <td class="col-check" onClick={e => e.stopPropagation()}>
                <input
                  type="checkbox"
                  checked={selected}
                  onChange={() => onToggleSelect(msg.id)}
                  aria-label={`Select message from ${senderName(msg.from_addr)}`}
                />
              </td>
              <td class="col-from">{senderName(msg.from_addr)}</td>
              <td class="col-subject">
                {snippets ? (
                  <>
                    <div class="subject-line">
                      <span class="subject-text">{msg.subject || '(no subject)'}</span>
                      <Badges msg={msg} />
                    </div>
                    {snippets[msg.id] && (
                      <div class="msg-snippet">{renderSnippet(snippets[msg.id])}</div>
                    )}
                  </>
                ) : (
                  <>
                    <span class="subject-text">{msg.subject || '(no subject)'}</span>
                    <Badges msg={msg} />
                  </>
                )}
              </td>
              {folderMap && (
                <td class="col-folder">{folderMap.get(msg.folder_id) ?? ''}</td>
              )}
              <td class="col-date" title={title}>{display}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
