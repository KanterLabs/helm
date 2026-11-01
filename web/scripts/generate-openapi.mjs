import { mkdir, readdir, readFile, rm, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, relative, resolve } from 'node:path';
import { loadOpenAPI } from './openapi-source.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const sourcePath = resolve(here, '../../openapi.yaml');
const outputDirectory = resolve(here, '../../internal/httpapi/openapi');
const checking = process.argv.includes('--check');
const { fragments } = loadOpenAPI(sourcePath);
const expected = new Map(fragments.map(({ name, document }) => [`${name}.json`, `${JSON.stringify(document, null, 2)}\n`]));

async function files(directory) {
  const entries = await readdir(directory, { withFileTypes: true }).catch((error) => {
    if (error.code === 'ENOENT') return [];
    throw error;
  });
  return (await Promise.all(entries.map(async (entry) => entry.isDirectory()
    ? files(resolve(directory, entry.name)) : [resolve(directory, entry.name)]))).flat();
}

for (const [name, generated] of expected) {
  if (Buffer.byteLength(generated) > 20_000) throw new Error(`${name} exceeds 20,000 bytes; split its source domain`);
}
const stale = [];
for (const [name, generated] of expected) {
  const outputPath = resolve(outputDirectory, name);
  if (checking) {
    if (await readFile(outputPath, 'utf8').catch(() => '') !== generated) stale.push(name);
  } else {
    await mkdir(dirname(outputPath), { recursive: true });
    await writeFile(outputPath, generated);
  }
}
for (const existing of await files(outputDirectory)) {
  if (expected.has(relative(outputDirectory, existing))) continue;
  if (checking) stale.push(relative(outputDirectory, existing));
  else await rm(existing);
}
if (stale.length) {
  console.error(`OpenAPI fragments are stale (${stale.join(', ')}); run npm run openapi:generate`);
  process.exitCode = 1;
}
