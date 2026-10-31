import { expect, type Browser, type TestInfo } from '@playwright/test';
import {
  attachCapture, attachScreenshot, baseURL, confirm, cookieWorks, createMemberViaApi, createProject, isPath, listSessions,
  openEditor, owner, runId, signedInContext, syntheticPassword
} from './membershipFixture';

// Real local-auth journey: self password change, administrator reset and
// administrator/self session revocation. Each step checks which actual
// browser cookies still authenticate; nothing here is route-mocked.
export async function passwordAndSessionJourney(browser: Browser, info: TestInfo): Promise<void> {
  const ownerContext = await signedInContext(browser, owner.email, owner.password);
  const project = await createProject(ownerContext, 'Pass');
  const id = runId();
  const first = syntheticPassword(`casey-1-${id}`); const second = syntheticPassword(`casey-2-${id}`); const third = syntheticPassword(`casey-3-${id}`);
  const member = await createMemberViaApi(ownerContext, { email: `casey.${id}@example.test`, name: `Casey Member ${id}`, password: first, project_ids: [project.id] });
  // Distinct user agents are real session metadata the UI must display.
  const device = (agent: string, password: string) => signedInContext(browser, member.email!, password, agent);
  const laptop = await device('TC113 Laptop', first);
  const tablet = await device('TC113 Tablet', first);
  const page = await laptop.newPage();
  const passwordKeys: Array<string | undefined> = [];
  page.on('request', (request) => { if (isPath(request.url(), /password$/)) passwordKeys.push(request.headers()['idempotency-key']); });
  await page.goto('/settings');
  const own = page.getByRole('region', { name: 'Signed-in sessions' });
  await expect(own.getByRole('listitem').filter({ hasText: 'TC113' })).toHaveCount(2);
  await expect(own).not.toContainText(/current session|this device/i);

  const form = page.getByRole('form', { name: 'Change password' });
  await form.getByLabel('Current password').fill(first);
  await form.getByLabel('New password', { exact: true }).fill(second);
  await form.getByLabel('Confirm new password').fill(`${second}x`);
  await form.getByRole('button', { name: 'Change password' }).click();
  await expect(form.getByRole('alert')).toContainText('do not match');
  expect(passwordKeys, 'mismatched confirmation never reaches the server').toEqual([]);
  await form.getByLabel('Current password').fill(syntheticPassword(`wrong-${id}`));
  await form.getByLabel('Confirm new password').fill(second);
  await form.getByRole('button', { name: 'Change password' }).click();
  await expect(form.getByRole('alert')).toContainText('current password was not accepted');
  await form.getByLabel('Current password').fill(first);
  await form.getByRole('button', { name: 'Change password' }).click();
  await expect(form.getByRole('status')).toContainText('Password changed');
  // A failure must report a boolean, never echo a retained password value.
  expect(await form.getByLabel('Current password').inputValue() === '', 'password field cleared').toBe(true);
  expect(await cookieWorks(laptop), 'requesting session survives').toBe(true);
  expect(await cookieWorks(tablet), 'other sessions end').toBe(false);
  await page.reload();
  await expect(page.getByRole('region', { name: 'Your account' })).toBeVisible();

  const ownerPage = await ownerContext.newPage();
  await ownerPage.goto('/settings');
  const editor = await openEditor(ownerPage, member.name, member.email!);
  await editor.getByLabel(`New password for ${member.name}`, { exact: true }).fill(third);
  await editor.getByLabel(`Confirm new password for ${member.name}`).fill(third);
  await editor.getByRole('button', { name: 'Reset password' }).click();
  await confirm(ownerPage, 'Reset password');
  await expect(editor.getByRole('status')).toContainText('Password reset');
  expect(await cookieWorks(laptop), 'reset expires every member session').toBe(false);
  const oldLogin = await ownerContext.request.fetch('/api/v1/auth/login', { method: 'POST', headers: { Origin: new URL(baseURL).origin, Cookie: '' }, data: { email: member.email, password: second } });
  expect(oldLogin.status(), 'replaced password is rejected generically').toBe(401);

  const phone = await device('TC113 Phone', third);
  const desk = await device('TC113 Desk', third);
  const spare = await device('TC113 Spare', third);
  const sessions = editor.getByRole('region', { name: `Sessions for ${member.name}` });
  await sessions.getByRole('button', { name: /^(Show|Refresh) sessions$/ }).click();
  await expect(sessions.getByRole('listitem').filter({ hasText: 'TC113 Phone' })).toBeVisible();
  await sessions.getByRole('listitem').filter({ hasText: 'TC113 Phone' }).getByRole('button', { name: /Revoke/ }).click();
  await confirm(ownerPage, 'Revoke session');
  await expect(sessions.getByRole('status')).toContainText('Session revoked');
  await expect(sessions.getByRole('listitem').filter({ hasText: 'TC113 Phone' }).getByRole('button', { name: /Revoke/ })).toHaveCount(0);
  expect(await cookieWorks(phone)).toBe(false);
  expect(await cookieWorks(desk)).toBe(true);
  const adminView = await listSessions(ownerContext, member.id);

  const deskPage = await desk.newPage();
  await deskPage.goto('/settings');
  const deskSessions = deskPage.getByRole('region', { name: 'Signed-in sessions' });
  await deskSessions.getByRole('listitem').filter({ hasText: 'TC113 Spare' }).getByRole('button', { name: /Revoke/ }).click();
  await confirm(deskPage, 'Revoke session');
  await expect(deskSessions.getByRole('status')).toContainText('Session revoked');
  expect(await cookieWorks(spare)).toBe(false);
  expect(await cookieWorks(desk)).toBe(true);
  await deskSessions.getByRole('listitem').filter({ hasText: 'TC113 Desk' }).getByRole('button', { name: /Revoke/ }).click();
  await confirm(deskPage, 'Revoke session');
  await expect(deskPage.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible();
  expect(passwordKeys.length).toBeGreaterThan(0);
  expect(passwordKeys.every((key) => key === undefined), 'password routes never send Idempotency-Key').toBe(true);
  for (const session of adminView) expect(Object.keys(session).some((key) => /token|hash|current/i.test(key))).toBe(false);
  await attachCapture(info, 'real-session-descriptors.json', adminView.map(({ id: sessionId, created_at, last_seen_at, expires_at, revoked_at, user_agent }) => ({ id: sessionId, created_at, last_seen_at, expires_at, revoked_at, user_agent })));
  await attachScreenshot(info, ownerPage, 'real-admin-sessions.png');
  await Promise.all([ownerContext, laptop, tablet, phone, desk, spare].map((context) => context.close()));
}
