import { splitAddressList } from './address.js';

export interface MailtoFields {
  to: string[];
  cc: string[];
  bcc: string[];
  subject: string;
  body: string;
}

/** Decode RFC 6068 fields once; '+' is literal, never a form-encoded space. */
export function parseMailto(uri: string): MailtoFields | null {
  if (!/^mailto:/i.test(uri)) return null;
  const fields: MailtoFields = { to: [], cc: [], bcc: [], subject: '', body: '' };
  const raw = uri.slice(7);
  const question = raw.indexOf('?');
  const addresses = question < 0 ? raw : raw.slice(0, question);
  const query = question < 0 ? '' : raw.slice(question + 1);
  try {
    const add = (key: 'to' | 'cc' | 'bcc', value: string) => {
      // Header line breaks must not become injected headers on send.
      if (!/[\r\n\0]/.test(value)) fields[key].push(...splitAddressList(value).filter(Boolean));
    };
    add('to', decodeURIComponent(addresses));
    for (const pair of query.split('&')) {
      const equals = pair.indexOf('=');
      if (equals < 0) continue;
      const key = decodeURIComponent(pair.slice(0, equals)).toLowerCase();
      const value = decodeURIComponent(pair.slice(equals + 1));
      if (key === 'to' || key === 'cc' || key === 'bcc') add(key, value);
      else if (key === 'subject' && !/[\r\n\0]/.test(value)) fields.subject = value;
      else if (key === 'body') fields.body = value.replace(/\r\n?/g, '\n').replace(/\0/g, '');
    }
    return fields;
  } catch {
    return null;
  }
}

/** Retain the deployment path while discarding existing query/fragment state. */
export function mailtoHandlerUrl(href: string): string {
  const url = new URL(href);
  url.search = '';
  url.hash = '';
  return `${url.href}#/compose?mailto=%s`;
}
