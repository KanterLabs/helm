#!/usr/bin/env node
// Serves deploy/cloudflare-connect-relay/worker.js on a loopback port so the
// E2E sign-in flow runs the real relay code. Usage: relay-server.mjs PORT
import { createServer } from 'node:http';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dirname, resolve } from 'node:path';

const port = Number(process.argv[2]);
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const worker = (await import(pathToFileURL(resolve(root, 'deploy/cloudflare-connect-relay/worker.js')).href)).default;

createServer(async (incoming, outgoing) => {
  const response = await worker.fetch(new Request(`http://127.0.0.1:${port}${incoming.url}`, { method: incoming.method }));
  outgoing.writeHead(response.status, Object.fromEntries(response.headers));
  outgoing.end(Buffer.from(await response.arrayBuffer()));
}).listen(port, '127.0.0.1');
