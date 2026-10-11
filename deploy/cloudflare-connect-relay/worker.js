// Helm "Sign in with Cloudflare" relay. Cloudflare OAuth clients need one
// exact redirect URL, but every self-hosted Helm has its own address, so the
// client's redirect is this Worker. It sends the browser back to the Helm
// named in `state` (`<nonce>.<base64url origin>`), after showing where.
//
// It never sees a usable credential: the authorization code is worthless
// without the PKCE verifier, which never leaves the Helm that started
// sign-in. It does not redirect automatically, so a crafted link cannot
// silently send someone's sign-in to an unexpected site.
// Design: docs/CLOUDFLARE_CONNECT_PLAN.md. Deploy: ./deploy.sh.

const CALLBACK = "/api/v1/cloudflare/oauth/callback";
const FORWARDED = ["code", "state", "error", "error_description"];

export default {
  async fetch(request) {
    const url = new URL(request.url);
    if (request.method !== "GET") return page(405, "Not allowed", "<p>This page only accepts sign-in returns.</p>");
    if (url.pathname === "/cloudflare/callback") return callback(url);
    if (url.pathname === "/") {
      return page(200, "Helm sign-in relay", "<p>This page returns you to your own Helm after you sign in with Cloudflare. There is nothing to do here.</p>");
    }
    return page(404, "Not found", "<p>Nothing here.</p>");
  },
};

// helmOrigin decodes and checks the Helm address from state.
export function helmOrigin(state) {
  const dot = (state || "").indexOf(".");
  if (dot < 1) return null;
  try {
    const encoded = state.slice(dot + 1).replace(/-/g, "+").replace(/_/g, "/");
    const origin = atob(encoded + "===".slice((encoded.length + 3) % 4));
    const parsed = new URL(origin);
    const loopback = parsed.hostname === "localhost" || parsed.hostname === "127.0.0.1";
    if (parsed.protocol !== "https:" && !(parsed.protocol === "http:" && loopback)) return null;
    if (parsed.username || parsed.password || parsed.origin !== origin) return null;
    return parsed.origin;
  } catch {
    return null;
  }
}

function callback(url) {
  const origin = helmOrigin(url.searchParams.get("state"));
  if (!origin) {
    return page(400, "This sign-in link is not valid", "<p>Start <strong>Sign in with Cloudflare</strong> again from Helm.</p>");
  }
  const target = new URL(CALLBACK, origin);
  for (const key of FORWARDED) {
    const value = url.searchParams.get(key);
    if (value !== null) target.searchParams.set(key, value.slice(0, 2000));
  }
  const failed = url.searchParams.has("error");
  return page(200, failed ? "Cloudflare sign-in was not completed" : "Finish connecting Cloudflare",
    `<p>${failed ? "Cloudflare did not grant access. Return to Helm to see why." : "Cloudflare sign-in finished. Continue to your Helm at:"}</p>
     <p class="host" data-helm-origin>${escapeHTML(origin)}</p>
     <p class="note">Only continue if this is your Helm and you just started “Sign in with Cloudflare” there. Otherwise close this page.</p>
     <a class="button" id="continue" href="${escapeHTML(target.href)}">Continue to Helm</a>`);
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[char]);
}

function page(status, title, body) {
  const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>${escapeHTML(title)} · Helm</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f6f7fb;color:#1d2433;font:15px/1.5 system-ui,sans-serif}main{max-width:460px;margin:24px;padding:28px;border:1px solid #dde1ea;border-radius:14px;background:#fff}h1{margin:0 0 12px;font-size:20px}.host{padding:10px 12px;border-radius:8px;background:#f0f1f6;font:600 14px ui-monospace,monospace;overflow-wrap:anywhere}.note{color:#4e586a;font-size:13px}.button{display:inline-block;margin-top:8px;padding:10px 16px;border-radius:9px;background:#6d5efc;color:#fff;font-weight:700;text-decoration:none}</style>
</head><body><main><h1>${escapeHTML(title)}</h1>${body}</main></body></html>`;
  return new Response(html, {
    status,
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store",
      "Referrer-Policy": "no-referrer",
      "Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
      "X-Content-Type-Options": "nosniff",
    },
  });
}
