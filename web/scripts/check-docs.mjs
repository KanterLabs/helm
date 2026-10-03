#!/usr/bin/env node
// Documentation checks (blocking in `npm run check` and `make lint`).
// See docs/CLOUDFLARE_CONNECT_PLAN.md § Documentation standard.
//
// 1. Operation names in user guides exist in openapi.yaml, and every
//    webhook / public-access operation is named in a user guide.
// 2. Relative links and #anchors in tracked docs resolve.
// 3. Every tracked docs/*_PLAN.md starts with a Status: line.
// 4. Every HELM_* setting read in internal/config is documented.
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const userGuides = ['docs/PUBLIC_ACCESS.md', 'docs/TICKET_WEBHOOKS.md'];
const guidedPaths = /^\/api\/v1\/(hooks|ticket-webhooks|public-endpoints|email-intake|cloudflare|docs|intake)\b/;
const operationName = /^(get|post|create|list|rotate|disable|set|send|test|start|finish|connect|disconnect|receive)[A-Z]\w+$/;
const problems = [];

// The production image's frontend stage copies only web/ and the OpenAPI
// files; the docs are checked by CI and `make lint` on the full checkout.
if (!existsSync(join(root, 'docs')) || !existsSync(join(root, 'internal/config'))) {
  console.log('Documentation check skipped: repository docs are not present (frontend-only build).');
  process.exit(0);
}

function trackedMarkdown() {
  try {
    return execFileSync('git', ['ls-files', '--', 'docs/*.md', 'deploy/**/*.md', 'README.md'], { cwd: root, encoding: 'utf8' })
      .split('\n').filter(Boolean);
  } catch {
    return readdirSync(join(root, 'docs')).filter((name) => name.endsWith('.md')).map((name) => `docs/${name}`);
  }
}

const read = (file) => readFileSync(join(root, file), 'utf8');

// GitHub's heading anchor algorithm.
export function slug(heading) {
  return heading.trim().toLowerCase().replace(/<[^>]+>/g, '').replace(/[^\p{L}\p{N}\s_-]/gu, '').replace(/\s/g, '-');
}

function anchors(file) {
  const seen = new Map();
  const result = new Set();
  let fenced = false;
  for (const line of read(file).split('\n')) {
    if (line.startsWith('```')) fenced = !fenced;
    for (const [, id] of line.matchAll(/<a\s+(?:id|name)="([^"]+)"/g)) result.add(id);
    const match = !fenced && /^#{1,6}\s+(.+?)\s*#*\s*$/.exec(line);
    if (!match) continue;
    const base = slug(match[1].replace(/`/g, '').replace(/\[([^\]]*)\]\([^)]*\)/g, '$1'));
    const count = seen.get(base) || 0;
    seen.set(base, count + 1);
    result.add(count ? `${base}-${count}` : base);
  }
  return result;
}

// User guides are always checked, even before they are first committed.
const files = [...new Set([...trackedMarkdown(), ...userGuides, 'docs/CONFIGURATION.md'])];

// 1. OpenAPI operation names.
const spec = parse(read('openapi.yaml'));
const operations = new Map();
for (const [path, item] of Object.entries(spec.paths || {})) {
  for (const operation of Object.values(item)) {
    if (operation && typeof operation === 'object' && operation.operationId) operations.set(operation.operationId, path);
  }
}
const named = new Set();
for (const guide of userGuides) {
  for (const [, token] of read(guide).matchAll(/`([^`\s]+)`/g)) {
    if (!operationName.test(token)) continue;
    named.add(token);
    if (!operations.has(token)) problems.push(`${guide}: names operation \`${token}\`, which is not in openapi.yaml`);
  }
}
for (const [id, path] of operations) {
  if (guidedPaths.test(path) && !named.has(id)) problems.push(`openapi.yaml: operation ${id} (${path}) is not named in ${userGuides.join(' or ')}`);
}

// 2. Relative links and anchors.
for (const file of files) {
  let fenced = false;
  read(file).split('\n').forEach((line, index) => {
    if (line.startsWith('```')) fenced = !fenced;
    if (fenced) return;
    for (const [, target] of line.replace(/`[^`]*`/g, '').matchAll(/\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g)) {
      if (/^[a-z]+:/i.test(target) || target.startsWith('/')) continue;
      const [pathPart, anchor] = target.split('#');
      const destination = pathPart ? relative(root, resolve(dirname(join(root, file)), pathPart)) : file;
      if (!existsSync(join(root, destination))) {
        problems.push(`${file}:${index + 1}: link to missing file ${target}`);
        continue;
      }
      if (anchor && destination.endsWith('.md') && !anchors(destination).has(anchor)) {
        problems.push(`${file}:${index + 1}: link to missing heading #${anchor} in ${destination}`);
      }
    }
  });
}

// 3. Plan status lines.
for (const file of files.filter((name) => /^docs\/[^/]+_PLAN\.md$/.test(name))) {
  if (!read(file).split('\n').slice(0, 12).some((line) => /^Status:/.test(line))) {
    problems.push(`${file}: plans must start with a "Status:" line (within the first 12 lines)`);
  }
}

// 4. Settings are documented.
const documented = files.map(read).join('\n');
const configDir = join(root, 'internal/config');
for (const name of readdirSync(configDir).filter((file) => file.endsWith('.go') && !file.endsWith('_test.go'))) {
  for (const [, variable] of readFileSync(join(configDir, name), 'utf8').matchAll(/"(HELM_[A-Z0-9_]+)"/g)) {
    if (!documented.includes(variable)) problems.push(`internal/config/${name}: setting ${variable} is not documented in any tracked doc`);
  }
}

const unique = [...new Set(problems)];
if (unique.length) {
  console.error(`Documentation check failed (${unique.length}):\n- ${unique.join('\n- ')}`);
  process.exit(1);
}
console.log(`Documentation check passed: ${files.length} docs, ${operations.size} operations.`);
