# Unreleased (on `beta`)

User-visible changes on `beta` since the last release, newest first. At the
next release these entries move into `docs/releases/vX.Y.Z.md`.

## 2026-10-04

- **Simpler Connect apps.** One **Reach Helm from outside** panel centred on
  **Sign in with Cloudflare**; after setup it shows a short summary, with
  details and manual setup folded away. Webhook rows show **Send test** and
  **Test email**, other actions under **More**.
- **Test email.** A one-time address per webhook to send any email to from
  your own mailbox; Helm shows when it arrived and the test ticket it filed.

## 2026-10-03

- **Connect Cloudflare.** Tickets → Connect apps has a guided setup: sign in
  with Cloudflare (or paste a token from a pre-filled link), pick a domain,
  and Helm creates the public URL and email addresses with a live checklist,
  then forgets the credential. Guide: [PUBLIC_ACCESS.md](../PUBLIC_ACCESS.md).
- **In-app help.** Every Connect apps card has **Learn more**, which opens the
  user guide inside Helm.
- **Clearer manual setup.** Manual Public URL and Email forms moved under
  **Set up manually**; entering a bare domain now offers `hooks.<domain>`.
- **Send test** on a webhook opens a real ticket through the public URL.
- **Email addresses for webhooks** through Cloudflare Email Routing, with
  Recent emails and bounce reasons.
- **Public URL status:** live connector details, a self-test, history and
  "Finish cleanup" for leftovers in Cloudflare.
- **Documentation checks** run in `npm run check` and `make lint`: operation
  names, links, plan status lines and settings
  ([CONFIGURATION.md](../CONFIGURATION.md)).
