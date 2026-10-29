import { defineConfig, devices } from '@playwright/test';
import path from 'node:path';

// TC-310 real-backend journeys. Run through test/e2e/column-audit/run.sh, which
// serves internal/httpapi/column_audit_browser_test.go on HELM_E2E_BASE_URL.
const artifactRoot = path.resolve(process.env.HELM_COLUMN_AUDIT_ARTIFACTS || 'e2e-artifacts/column-audit');

export default defineConfig({
  testDir: '.',
  testMatch: 'column-audit-*.spec.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list'], ['json', { outputFile: path.join(artifactRoot, 'results.json') }]],
  outputDir: path.join(artifactRoot, 'test-results'),
  use: { baseURL: process.env.HELM_E2E_BASE_URL || 'http://127.0.0.1:18310', trace: 'on', screenshot: 'only-on-failure' },
  expect: { timeout: 10_000 },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    // Representative WebKit coverage: the layout/keyboard journey only.
    { name: 'webkit', use: { ...devices['Desktop Safari'] }, grep: /@webkit/ }
  ]
});
