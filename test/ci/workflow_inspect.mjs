/** Parse actual YAML, recursively loading local workflow/action dependencies. */
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
const root = path.resolve(process.argv[2] || '.');
const require = createRequire(path.join(root, 'web/package.json'));
const YAML = require('yaml');
const documents = {};
const scripts = {};
const scriptDependencies = {};
function loadScript(relative) {
  if (scripts[relative]) return;
  const filename = path.resolve(root, relative);
  if (!filename.startsWith(root + path.sep)) throw new Error('external script');
  const source = fs.readFileSync(filename, 'utf8');
  scripts[relative] = source;
  scriptDependencies[relative] = [];
  const candidates = [...source.matchAll(/(?:^|\n)(?:from|import) ([A-Za-z_][A-Za-z_0-9]*)/g)]
    .map(match => match[1] + '.py');
  candidates.push(...[...source.matchAll(/['"]([A-Za-z_0-9-]+\.py)['"]/g)].map(match => match[1]));
  for (const name of new Set(candidates)) {
    const child = path.posix.join(path.posix.dirname(relative), name);
    if (fs.existsSync(path.join(root, child))) {
      scriptDependencies[relative].push(child);
      loadScript(child);
    }
  }
}
function load(relative) {
  const filename = path.resolve(root, relative);
  if (!filename.startsWith(root + path.sep)) throw new Error('external local YAML');
  if (documents[relative]) return;
  const document = YAML.parseDocument(fs.readFileSync(filename, 'utf8'), {uniqueKeys: true});
  if (document.errors.length) throw new Error(document.errors.map(e => e.message).join('\n'));
  const value = document.toJS();
  documents[relative] = value;
  function walk(item) {
    if (Array.isArray(item)) return item.forEach(walk);
    if (!item || typeof item !== 'object') return;
    if (typeof item.run === 'string') {
      for (const ref of item.run.matchAll(/\btest\/ci\/[A-Za-z0-9_./-]+\.(?:sh|py)\b/g)) {
        loadScript(ref[0]);
      }
    }
    if (typeof item.uses === 'string' && item.uses.startsWith('./')) {
      const ref = item.uses.slice(2);
      if (/\.ya?ml$/.test(ref)) load(ref);
      else {
        const action = ['action.yml', 'action.yaml'].find(name => fs.existsSync(path.join(root, ref, name)));
        if (!action) throw new Error('missing local action: ' + ref);
        load(path.posix.join(ref, action));
      }
    }
    Object.values(item).forEach(walk);
  }
  walk(value);
}
load('.github/workflows/ci.yml');
// Conditions are interpreted, never shell-executed. This deliberately supports
// the bounded GitHub expression grammar used in job admission predicates.
function evaluate(source, context) {
  source = String(source ?? 'success()').replace(/^\s*\$\{\{([\s\S]*)\}\}\s*$/, '$1');
  const tokens = source.match(/'(?:''|[^'])*'|&&|\|\||==|!=|[!(),.]|[A-Za-z_][A-Za-z_0-9-]*|\d+/g) || [];
  let index = 0;
  const all = Object.values(context.needs || {}).map(value => value.result);
  const functions = {
    always: () => true, success: () => all.every(value => value === 'success'),
    failure: () => all.includes('failure'), cancelled: () => all.includes('cancelled'),
    contains: (value, item) => value.includes(item), fromJSON: value => JSON.parse(value),
  };
  function atom() {
    const token = tokens[index++];
    if (token === '!') return !atom();
    if (token === '(') { const value = expression(); expect(')'); return value; }
    if (token?.startsWith("'")) return token.slice(1, -1).replaceAll("''", "'");
    if (token === 'true' || token === 'false') return token === 'true';
    if (token === 'null') return null;
    if (/^\d+$/.test(token)) return Number(token);
    if (tokens[index] === '(') {
      index++;
      const args = [];
      if (tokens[index] !== ')') {
        args.push(expression());
        while (tokens[index] === ',') { index++; args.push(expression()); }
      }
      expect(')');
      if (!functions[token]) throw new Error('unsupported expression function: ' + token);
      return functions[token](...args);
    }
    let value = context[token];
    while (tokens[index] === '.') { index++; value = value?.[tokens[index++]]; }
    return value ?? '';
  }
  function expect(token) {
    if (tokens[index++] !== token) throw new Error('expected expression token ' + token);
  }
  function equality() {
    let value = atom();
    while (tokens[index] === '==' || tokens[index] === '!=') {
      const operator = tokens[index++]; const other = atom();
      value = operator === '==' ? value == other : value != other;
    }
    return value;
  }
  function conjunction() {
    let value = equality();
    while (tokens[index] === '&&') { index++; const other = equality(); value = value && other; }
    return value;
  }
  function expression() {
    let value = conjunction();
    while (tokens[index] === '||') { index++; const other = conjunction(); value = value || other; }
    return value;
  }
  const explicitStatus = /\b(always|success|failure|cancelled)\s*\(/.test(source);
  const value = expression();
  if (index !== tokens.length) throw new Error('unsupported expression tail: ' + tokens.slice(index));
  return Boolean(value) && (explicitStatus || functions.success());
}
if (process.argv.includes('--conditions')) {
  const queries = JSON.parse(fs.readFileSync(0, 'utf8'));
  const jobs = documents['.github/workflows/ci.yml'].jobs;
  const results = queries.map(query => {
    const job = jobs[query.job];
    if (!job) throw new Error('unknown workflow job: ' + query.job);
    const dependencies = typeof job.needs === 'string' ? [job.needs] : (job.needs || []);
    const context = {...query, needs: Object.fromEntries(dependencies.map(name =>
      [name, query.needs?.[name] || {result: 'success', outputs: {}}]))};
    return {job: query.job, condition: job.if || 'success()', admitted: evaluate(job.if, context)};
  });
  process.stdout.write(JSON.stringify(results, null, 2) + '\n');
} else {
  process.stdout.write(JSON.stringify({documents, scripts, scriptDependencies}, null, 2) + '\n');
}
