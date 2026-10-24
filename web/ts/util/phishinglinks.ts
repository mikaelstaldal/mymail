/** Document parsing for the demo worker, which has no DOM API. The inert
 * template does not load external images or execute message content. */
export function phishingLinks(body: string): Array<{ Index: number; Text: string; Href: string }> {
  const template = document.createElement('template');
  template.innerHTML = body;
  const links = [];
  let index = 0;
  for (const a of template.content.querySelectorAll('a')) {
    if (index >= 1000) { links.push({ Index: index, Text: '', Href: '' }); break; }
    const text: string[] = [];
    const stack: Node[] = Array.from(a.childNodes).reverse();
    while (stack.length) {
      const n = stack.pop()!;
      if (n.nodeType === 1 && ['SCRIPT', 'STYLE'].includes((n as Element).tagName)) continue;
      if (n.nodeType === 3) text.push(n.textContent ?? '');
      for (let i = n.childNodes.length - 1; i >= 0; i--) stack.push(n.childNodes[i]);
    }
    links.push({ Index: index++, Text: text.join(''), Href: a.getAttribute('href') ?? '' });
  }
  return links;
}

export function rewritePhishingLinks(body: string, id: number, indices: number[], base: string): string {
  const template = document.createElement('template');
  template.innerHTML = body;
  // Index 1000 is the overflow sentinel, not an assessed link.
  const flagged = new Set(indices);
  let index = 0;
  for (const a of template.content.querySelectorAll('a')) {
    if (flagged.has(1000) && index >= 1000) a.removeAttribute('href');
    else if (flagged.has(index)) a.setAttribute('href', `${base}#/link/${id}/${index}`);
    index++;
  }
  return template.innerHTML;
}
