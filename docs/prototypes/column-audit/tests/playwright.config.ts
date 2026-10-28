import { defineConfig } from '@playwright/test';
import path from 'node:path';

// Standalone prototype suite: loads index.html over file://, no server.
const root = path.resolve(__dirname, '..');

export default defineConfig({
  testDir: __dirname,
  testMatch: '*.spec.ts',
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: [['list'], ['json', { outputFile: path.join(root, 'out', 'results.json') }]],
  outputDir: path.join(root, 'out', 'test-results'),
  timeout: 60_000,
  expect: { timeout: 6_000 },
  use: {
    browserName: 'chromium',
    viewport: { width: 1280, height: 860 },
    trace: 'on',
    screenshot: 'off',
    reducedMotion: 'reduce'
  }
});
