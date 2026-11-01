import { expect, test, type Page } from '@playwright/test';
import {
  agentGroup,
  attachMaskedScreenshot,
  attachSanitized,
  createAgent,
  createProject,
  expectNoHorizontalOverflow,
  expectPlaintextGone,
  openAgentSettings,
  requireDisabledAuth,
  revealedValue,
  runId
} from './support/agentTokens';

// The reveal renders plaintext: no automatic traces, videos or screenshots.
test.use({ trace: 'off', video: 'off', screenshot: 'off' });

async function issue(page: Page, agent: { id: string; name: string }, project: { name: string }, name: string) {
  const trigger = agentGroup(page, agent.id).getByRole('button', { name: `New token for ${agent.name}`, exact: true });
  await trigger.click();
  const dialog = page.getByRole('dialog', { name: `New token for ${agent.name}` });
  await dialog.getByLabel('Token name').fill(name);
  await dialog.getByRole('checkbox', { name: project.name, exact: true }).check();
  await dialog.getByRole('button', { name: 'Generate token', exact: true }).click();
  const reveal = page.getByRole('dialog', { name: 'Save your new token' });
  await expect(reveal).toBeVisible();
  return { trigger, reveal, plaintext: await revealedValue(page) };
}

test.describe('one-time token reveal', () => {
  test('copy, setup guidance and Done restore focus and leave no plaintext behind', async ({ page, context, request }, testInfo) => {
    await requireDisabledAuth(request);
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    const id = runId();
    const project = await createProject(request, `TV${id}`.slice(0, 16), `Reveal ${id}`);
    const agent = await createAgent(request, `Reveal ${id}`);
    await page.setViewportSize({ width: 1280, height: 900 });
    await openAgentSettings(page);
    const { trigger, reveal, plaintext } = await issue(page, agent, project, `Reveal ${id}`);

    await expect(reveal).toContainText(agent.name);
    await expect(reveal).toContainText(`Reveal ${id}`);
    await expect(reveal).toContainText(project.name);
    await expect(reveal).toContainText(/Expires/);
    const done = reveal.getByRole('button', { name: 'Done', exact: true });
    await expect(done).toBeDisabled();

    // Setup guidance uses placeholders and the canonical skill contract only.
    for (const label of ['Helm skill', 'Environment', 'CI']) await reveal.getByText(label, { exact: true }).click();
    const guidance = reveal.locator('[data-token-setup]');
    await expect(guidance).toContainText('~/.config/helm/credentials.json');
    await expect(guidance).toContainText('0600');
    await expect(guidance).toContainText('HELM_URL');
    await expect(guidance).toContainText('HELM_TOKEN');
    await expect(guidance).toContainText('auth-check');
    await expect(guidance).toContainText(new URL(page.url()).origin);
    expect(await guidance.innerText()).not.toContain(plaintext);
    await expect(reveal.locator('a[download], a[href*="data:"], a[href*="blob:"]')).toHaveCount(0);

    await reveal.getByRole('button', { name: 'Copy token', exact: true }).click();
    await expect(reveal.getByRole('status')).toContainText('Copied');
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(plaintext);
    await expect(done).toBeEnabled();
    await attachMaskedScreenshot(page, testInfo, 'reveal-desktop.png');
    await page.setViewportSize({ width: 320, height: 720 });
    await expectNoHorizontalOverflow(page);
    await attachMaskedScreenshot(page, testInfo, 'reveal-320.png');
    await page.setViewportSize({ width: 1280, height: 900 });
    await done.click();
    await expect(reveal).toBeHidden();
    await expect(trigger).toBeFocused();
    await expectPlaintextGone(page, plaintext);
    await page.evaluate(() => navigator.clipboard.writeText(''));
    await page.reload();
    await expectPlaintextGone(page, plaintext);
    await attachSanitized(testInfo, 'reveal-flow.json', { copied: true, focusRestored: true, plaintextPersisted: false }, [plaintext]);
  });

  test('denied clipboard, Escape and deliberate discard keep the secret under user control', async ({ page, request }) => {
    await requireDisabledAuth(request);
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: () => Promise.reject(new DOMException('denied', 'NotAllowedError')) } });
    });
    const id = runId();
    const project = await createProject(request, `TX${id}`.slice(0, 16), `Discard ${id}`);
    const agent = await createAgent(request, `Discard ${id}`);
    await openAgentSettings(page);
    const { trigger, reveal, plaintext } = await issue(page, agent, project, `Discard ${id}`);

    await reveal.getByRole('button', { name: 'Copy token', exact: true }).click();
    await expect(reveal.locator('[data-token-manual-copy]')).toContainText('Select the token');
    const field = reveal.getByRole('textbox', { name: 'Token', exact: true });
    await expect(field).toBeFocused();
    expect(await field.evaluate((input: HTMLInputElement) => input.selectionEnd! - input.selectionStart!)).toBe(plaintext.length);
    await expect(reveal.getByRole('button', { name: 'Done', exact: true })).toBeDisabled();

    // Backdrop clicks do nothing; Escape asks before discarding.
    await page.mouse.click(5, 5);
    await expect(reveal).toBeVisible();
    await page.keyboard.press('Escape');
    const confirm = page.getByRole('alertdialog', { name: 'Close without saving?' });
    await expect(confirm).toBeVisible();
    await confirm.getByRole('button', { name: 'Keep token open', exact: true }).click();
    await expect(reveal).toBeVisible();
    expect(await field.inputValue()).toBe(plaintext);

    await reveal.getByRole('button', { name: 'Close without saving', exact: true }).click();
    await confirm.getByRole('button', { name: 'Discard token', exact: true }).click();
    await expect(reveal).toBeHidden();
    await expect(trigger).toBeFocused();
    await expectPlaintextGone(page, plaintext);
  });

  test('navigation, unload, session loss and PWA activation respect the secret lifetime', async ({ page, request }) => {
    await requireDisabledAuth(request);
    const id = runId();
    const project = await createProject(request, `TN${id}`.slice(0, 16), `Guard ${id}`);
    const agent = await createAgent(request, `Guard ${id}`);
    await page.goto('/');
    await openAgentSettings(page);
    const { reveal, plaintext } = await issue(page, agent, project, `Guard ${id}`);

    // In-app back navigation is held while the reveal is unsaved.
    await page.goBack();
    await expect(reveal).toBeVisible();
    expect(new URL(page.url()).pathname).toBe('/settings');
    expect(page.url()).not.toContain(plaintext);

    // PWA activation is blocked while the reveal is open.
    expect(await page.evaluate(() => document.documentElement.dataset.helmSecretOpen)).toBe('true');

    // Best-effort unload protection is armed.
    const prevented = await page.evaluate(() => {
      const event = new Event('beforeunload', { cancelable: true });
      window.dispatchEvent(event);
      return event.defaultPrevented;
    });
    expect(prevented).toBe(true);

    // A session loss clears the plaintext immediately.
    await page.evaluate(() => window.dispatchEvent(new Event('helm:auth-invalidated')));
    await expect(reveal).toBeHidden();
    await expectPlaintextGone(page, plaintext);
    expect(await page.evaluate(() => document.documentElement.dataset.helmSecretOpen || '')).toBe('');
  });
});
