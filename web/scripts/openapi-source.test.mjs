import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { loadOpenAPI } from './openapi-source.mjs';

function fixture(t, fragments, sources) {
  const root = mkdtempSync(join(tmpdir(), 'helm-openapi-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(join(root, 'openapi'));
  writeFileSync(join(root, 'openapi.yaml'), `openapi: 3.1.0\nx-helm-fragments: ${JSON.stringify(fragments)}\n`);
  for (const [name, content] of Object.entries(sources)) writeFileSync(join(root, 'openapi', name), content);
  return join(root, 'openapi.yaml');
}

test('assembles domains without leaking authoring metadata or rewriting internal refs', (t) => {
  const source = fixture(t, ['openapi/tasks.yaml', 'openapi/models.yaml'], {
    'tasks.yaml': "paths:\n  /tasks:\n    get:\n      operationId: listTasks\n      responses:\n        '200':\n          $ref: '#/components/responses/OK'\n",
    'models.yaml': "components:\n  responses:\n    OK:\n      description: Success\n",
  });
  const { document, fragments } = loadOpenAPI(source);
  assert.equal(document.openapi, '3.1.0');
  assert.equal(document['x-helm-fragments'], undefined);
  assert.equal(document.paths['/tasks'].get.responses['200'].$ref, '#/components/responses/OK');
  assert.equal(document.components.responses.OK.description, 'Success');
  assert.equal(fragments.length, 3);
});

test('rejects duplicate path definitions instead of silently changing operations', (t) => {
  const source = fixture(t, ['openapi/one.yaml', 'openapi/two.yaml'], {
    'one.yaml': 'paths:\n  /tasks: {}\n', 'two.yaml': 'paths:\n  /tasks: {}\n',
  });
  assert.throws(() => loadOpenAPI(source), /duplicate.*\/tasks/i);
});

test('rejects duplicate component definitions across domains', (t) => {
  const source = fixture(t, ['openapi/one.yaml', 'openapi/two.yaml'], {
    'one.yaml': 'components:\n  schemas:\n    Task: {}\n',
    'two.yaml': 'components:\n  schemas:\n    Task: {}\n',
  });
  assert.throws(() => loadOpenAPI(source), /duplicate.*Task/i);
});

test('rejects fragments outside the contract directory', (t) => {
  const source = fixture(t, ['../outside.yaml'], {});
  assert.throws(() => loadOpenAPI(source), /fragment path/i);
});

test('rejects metadata in a domain fragment', (t) => {
  const source = fixture(t, ['openapi/one.yaml'], { 'one.yaml': 'info:\n  title: Override\n' });
  assert.throws(() => loadOpenAPI(source), /fragment.*info/i);
});

test('rejects a manifest that repeats a source', (t) => {
  const source = fixture(t, ['openapi/one.yaml', 'openapi/one.yaml'], { 'one.yaml': 'paths: {}\n' });
  assert.throws(() => loadOpenAPI(source), /duplicate.*fragment/i);
});

test('reports missing fragments', (t) => {
  const source = fixture(t, ['openapi/missing.yaml'], {});
  assert.throws(() => loadOpenAPI(source), /ENOENT/);
});

test('reserves the embedded metadata asset name', (t) => {
  const source = fixture(t, ['openapi/document.yaml'], { 'document.yaml': 'paths: {}\n' });
  assert.throws(() => loadOpenAPI(source), /fragment path/i);
});
