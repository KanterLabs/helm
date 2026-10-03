import { describe, expect, it } from 'vitest';
import { renderHelpMarkdown, slug } from './helpMarkdown';

describe('renderHelpMarkdown', () => {
  it('escapes HTML before formatting', () => {
    const html = renderHelpMarkdown('Hello <script>alert(1)</script> **bold** `<b>`');
    expect(html).toContain('&lt;script&gt;');
    expect(html).not.toContain('<script>');
    expect(html).toContain('<strong>bold</strong>');
    expect(html).toContain('<code>&lt;b&gt;</code>');
  });

  it('gives headings GitHub ids and keeps guide links in the drawer', () => {
    const html = renderHelpMarkdown('## Connect Cloudflare (guided setup)\n\nSee [the guide](PUBLIC_ACCESS.md#settings), [webhooks](TICKET_WEBHOOKS.md), [a plan](PLAN.md) and [site](https://example.com).');
    expect(html).toContain('<h3 id="connect-cloudflare-guided-setup">');
    expect(html).toContain('data-help-page="public-access" data-help-anchor="settings"');
    expect(html).toContain('data-help-page="ticket-webhooks"');
    expect(html).toContain(' a plan ');
    expect(html).toContain('href="https://example.com" target="_blank" rel="noopener noreferrer"');
    expect(slug('Public URL (Cloudflare Tunnel)')).toBe('public-url-cloudflare-tunnel');
  });

  it('keeps tables inside list items and continues lists across blank lines', () => {
    const html = renderHelpMarkdown('1. First\n\n   | Step | Does |\n   | --- | --- |\n   | A | B |\n\n   After the table.\n2. Second\n\n3. Third');
    expect(html).toContain('<ol><li>First<div class="help-table"><table>');
    expect(html).toContain('<td>A</td><td>B</td>');
    expect(html).toContain('<p>After the table.</p></li><li>Second</li><li>Third</li></ol>');
    expect(html).not.toContain('| Step');
  });

  it('renders tables, nested lists and fenced code', () => {
    const html = renderHelpMarkdown('| A | B |\n| --- | --- |\n| `x` | y |\n\n1. One\n   - nested\n2. Two\n\n```sh\necho "<hi>"\n```');
    expect(html).toContain('<table><thead><tr><th>A</th><th>B</th></tr></thead><tbody><tr><td><code>x</code></td><td>y</td></tr></tbody></table>');
    expect(html).toContain('<ol><li>One<ul><li>nested</li></ul></li><li>Two</li></ol>');
    expect(html).toContain('<pre><code>echo &quot;&lt;hi&gt;&quot;</code></pre>');
  });
});
