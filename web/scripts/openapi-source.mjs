// The authoring manifest keeps domains independent; served documents contain
// only the assembled OpenAPI contract, with existing internal references intact.
import { readFileSync } from 'node:fs';
import { dirname, resolve, sep } from 'node:path';
import { parse } from 'yaml';

function object(value, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`);
  return value;
}

function addDefinitions(destination, source, label) {
  for (const [name, value] of Object.entries(object(source, label))) {
    if (Object.hasOwn(destination, name)) throw new Error(`Duplicate ${label} definition: ${name}`);
    destination[name] = value;
  }
}

export function mergeOpenAPIFragment(document, fragment, label) {
  for (const [section, definitions] of Object.entries(object(fragment, label))) {
    if (section === 'paths') {
      addDefinitions(document.paths, definitions, `${label} paths`);
    } else if (section === 'components') {
      for (const [category, values] of Object.entries(object(definitions, `${label} components`))) {
        document.components[category] ??= {};
        addDefinitions(document.components[category], values, `${label} components/${category}`);
      }
    } else {
      throw new Error(`Fragment ${label} cannot define ${section}; use openapi.yaml for metadata`);
    }
  }
}

export function loadOpenAPI(sourcePath) {
  const document = object(parse(readFileSync(sourcePath, 'utf8')), sourcePath);
  const names = document['x-helm-fragments'];
  if (!Array.isArray(names) || names.length === 0) throw new Error('Missing x-helm-fragments manifest');
  delete document['x-helm-fragments'];
  document.paths ??= {};
  document.components ??= {};
  const fragments = [{ name: 'document', document: structuredClone(document) }];
  const seen = new Set();
  const root = dirname(sourcePath);
  const contractDirectory = `${resolve(root, 'openapi')}${sep}`;
  for (const name of names) {
    if (typeof name !== 'string' || !/^openapi\/(?:[a-zA-Z0-9-]+\/)*[a-zA-Z0-9-]+\.yaml$/.test(name)
      || name === 'openapi/document.yaml'
      || !resolve(root, name).startsWith(contractDirectory)) throw new Error(`Invalid fragment path: ${name}`);
    if (seen.has(name)) throw new Error(`Duplicate fragment: ${name}`);
    seen.add(name);
    const fragment = object(parse(readFileSync(resolve(root, name), 'utf8')), name);
    mergeOpenAPIFragment(document, fragment, name);
    fragments.push({ name: name.slice('openapi/'.length, -'.yaml'.length), document: fragment });
  }
  return { document, fragments };
}
