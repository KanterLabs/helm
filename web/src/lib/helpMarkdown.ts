// Renders Helm's own user guides (docs/*.md, embedded by the server) for the
// in-app help drawer. Everything is HTML-escaped first; only the Markdown
// this project's guides use is supported: headings with GitHub-style ids,
// paragraphs, nested lists, tables, fenced code, inline code, emphasis and
// links. Links to other embedded guides stay inside the drawer.

/** GitHub's heading anchor algorithm (matches web/scripts/check-docs.mjs). */
export function slug(heading: string): string {
  return heading.trim().toLowerCase().replace(/<[^>]+>/g, '').replace(/[^\p{L}\p{N}\s_-]/gu, '').replace(/\s/g, '-');
}

const helpPages: Record<string, string> = { 'PUBLIC_ACCESS.md': 'public-access', 'TICKET_WEBHOOKS.md': 'ticket-webhooks' };

function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char] as string);
}

function link(label: string, href: string): string {
  if (/^https?:\/\//i.test(href)) return `<a href="${escapeHTML(href)}" target="_blank" rel="noopener noreferrer">${label}</a>`;
  const [file, anchor = ''] = href.split('#');
  const name = file.split('/').pop() || '';
  const page = file ? helpPages[name] : '';
  if (page !== undefined && (page || anchor)) {
    return `<a href="#" data-help-page="${escapeHTML(page || '')}" data-help-anchor="${escapeHTML(anchor)}">${label}</a>`;
  }
  return label;
}

/** Inline Markdown on already-escaped text. */
function inline(text: string): string {
  const codes: string[] = [];
  let out = escapeHTML(text).replace(/`([^`]+)`/g, (_, code: string) => `\u0000${codes.push(`<code>${code}</code>`) - 1}\u0000`);
  out = out.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, label: string, href: string) => link(label, href.replace(/&amp;/g, '&')));
  out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>').replace(/(^|[^\w*])\*([^*\s][^*]*)\*(?!\w)/g, '$1<em>$2</em>');
  return out.replace(/\u0000(\d+)\u0000/g, (_, index: string) => codes[Number(index)]);
}

function table(rows: string[]): string {
  const cells = (row: string) => row.trim().replace(/^\||\|$/g, '').split(/(?<!\\)\|/).map((cell) => cell.trim().replace(/\\\|/g, '|'));
  const [head, , ...body] = rows;
  return `<div class="help-table"><table><thead><tr>${cells(head).map((cell) => `<th>${inline(cell)}</th>`).join('')}</tr></thead><tbody>${body
    .map((row) => `<tr>${cells(row).map((cell) => `<td>${inline(cell)}</td>`).join('')}</tr>`)
    .join('')}</tbody></table></div>`;
}

type Item = { indent: number; ordered: boolean; text: string[]; table: string[]; children: Item[] };

function list(lines: string[]): string {
  const root: Item = { indent: -1, ordered: false, text: [], table: [], children: [] };
  const stack: Item[] = [root];
  for (const line of lines) {
    const marker = /^(\s*)([-*]|\d+\.)\s+(.*)$/.exec(line);
    if (marker) {
      const indent = marker[1].length;
      while (stack.length > 1 && indent <= stack[stack.length - 1].indent) stack.pop();
      const item: Item = { indent, ordered: /\d/.test(marker[2]), text: [marker[3]], table: [], children: [] };
      stack[stack.length - 1].children.push(item);
      stack.push(item);
    } else if (stack.length > 1 && line.trim().startsWith('|')) {
      stack[stack.length - 1].table.push(line.trim());
    } else if (stack.length > 1) {
      const item = stack[stack.length - 1];
      // Text after an item's table is a new paragraph in that item.
      item.text.push(item.table.length && line.trim() ? `\n${line.trim()}` : line.trim());
    }
  }
  const render = (items: Item[]): string => {
    if (!items.length) return '';
    const tag = items[0].ordered ? 'ol' : 'ul';
    return `<${tag}>${items.map((item) => {
      const [lead, ...after] = item.text.filter(Boolean).join(' ').split(' \n');
      const table_ = item.table.length >= 2 ? table(item.table) : '';
      const rest = after.length ? `<p>${inline(after.join(' ').replace(/^\n/, ''))}</p>` : '';
      return `<li>${inline(lead.replace(/^\n/, ''))}${table_}${rest}${render(item.children)}</li>`;
    }).join('')}</${tag}>`;
  };
  return render(root.children);
}

/** Renders a guide. Heading ids follow GitHub, so doc anchors work. */
export function renderHelpMarkdown(source: string): string {
  const lines = source.replace(/\r\n/g, '\n').split('\n');
  const out: string[] = [];
  const seen = new Map<string, number>();
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (line.startsWith('```')) {
      const code: string[] = [];
      index++;
      while (index < lines.length && !lines[index].startsWith('```')) code.push(lines[index++]);
      index++;
      out.push(`<pre><code>${escapeHTML(code.join('\n'))}</code></pre>`);
      continue;
    }
    const heading = /^(#{1,6})\s+(.+?)\s*#*\s*$/.exec(line);
    if (heading) {
      const base = slug(heading[2].replace(/`/g, '').replace(/\[([^\]]*)\]\([^)]*\)/g, '$1'));
      const count = seen.get(base) || 0;
      seen.set(base, count + 1);
      const level = Math.min(heading[1].length + 1, 6);
      out.push(`<h${level} id="${escapeHTML(count ? `${base}-${count}` : base)}">${inline(heading[2])}</h${level}>`);
      index++;
      continue;
    }
    const anchor = /^<a id="([\w-]+)"><\/a>$/.exec(line.trim());
    if (anchor) {
      out.push(`<a id="${anchor[1]}"></a>`);
      index++;
      continue;
    }
    if (line.trim().startsWith('|') && /^\s*\|?\s*:?-+/.test(lines[index + 1] || '')) {
      const rows: string[] = [];
      while (index < lines.length && lines[index].trim().startsWith('|')) rows.push(lines[index++]);
      out.push(table(rows));
      continue;
    }
    if (/^\s*([-*]|\d+\.)\s+/.test(line)) {
      const block: string[] = [];
      while (index < lines.length && lines[index].trim() !== '' && !lines[index].startsWith('```')) {
        if (!/^\s/.test(lines[index]) && !/^([-*]|\d+\.)\s+/.test(lines[index])) break;
        block.push(lines[index++]);
      }
      // A blank line followed by an indented line or another item of the
      // same list continues the list.
      while (index + 1 < lines.length && lines[index].trim() === '' && !lines[index + 1].trim().startsWith('```') && (/^\s{2,}\S/.test(lines[index + 1]) || /^([-*]|\d+\.)\s+/.test(lines[index + 1]))) {
        index++;
        while (index < lines.length && lines[index].trim() !== '' && (/^\s/.test(lines[index]) || /^([-*]|\d+\.)\s+/.test(lines[index]))) block.push(lines[index++]);
      }
      out.push(list(block));
      continue;
    }
    if (line.trim() === '') {
      index++;
      continue;
    }
    const paragraph: string[] = [];
    while (index < lines.length && lines[index].trim() !== '' && !/^(#{1,6}\s|```|\s*\||\s*([-*]|\d+\.)\s)/.test(lines[index])) paragraph.push(lines[index++].trim());
    out.push(`<p>${inline(paragraph.join(' '))}</p>`);
  }
  return out.join('\n');
}
