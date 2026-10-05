// Helm email intake Worker. Helm deploys this file from its release binary
// (internal/publicendpoint/email-worker.js); edits made in Cloudflare are
// overwritten on the next setup. See docs/EMAIL_ALERT_INTAKE_PLAN.md.
//
// Bindings: HELM_INTAKE_URL (secret_text) is Helm's email route including
// its intake secret; FALLBACK_TO (plain_text, optional) is a verified
// Email Routing destination that receives mail Helm could not accept.

const MAX_BYTES = 1048576;
const RETRY_DELAYS_MS = [2000, 4000];
// Helm answers these with a reason the sender should see as a bounce.
const PERMANENT = new Set([400, 404, 413, 422]);

export default {
  async email(message, env) {
    const envelope = {
      "X-Helm-Envelope-From": message.from,
      "X-Helm-Envelope-To": message.to,
    };
    const auth = message.headers.get("Authentication-Results");
    // Only Cloudflare's own verdict is passed on; senders can forge others.
    if (auth && /^\s*mx\.cloudflare\.net\s*;/i.test(auth)) {
      envelope["X-Helm-Authentication-Results"] = auth.slice(0, 1000);
    }

    if (message.rawSize > MAX_BYTES) {
      // Tell Helm without the body so the refusal is listed, then bounce or
      // hand the message to the fallback mailbox.
      await post(env, {
        ...envelope,
        "X-Helm-Oversize": String(message.rawSize),
        "X-Helm-Subject": (message.headers.get("Subject") || "").slice(0, 300),
        "X-Helm-Message-Id": (message.headers.get("Message-ID") || "").slice(0, 300),
      }, null).catch(() => null);
      if (await fallback(message, env, "too-large")) return;
      message.setReject(`Message is larger than Helm's ${MAX_BYTES / 1048576} MiB limit`);
      return;
    }

    const raw = await new Response(message.raw).arrayBuffer();
    let failure = "";
    for (let attempt = 0; attempt <= RETRY_DELAYS_MS.length; attempt++) {
      if (attempt > 0) await sleep(RETRY_DELAYS_MS[attempt - 1]);
      let response;
      try {
        response = await post(env, { ...envelope, "Content-Type": "message/rfc822" }, raw);
      } catch {
        failure = "Helm unreachable";
        continue;
      }
      if (response.ok) return;
      if (PERMANENT.has(response.status)) {
        message.setReject(await reason(response));
        return;
      }
      failure = `Helm HTTP ${response.status}`;
    }
    if (await fallback(message, env, failure)) return;
    throw new Error(`Helm email intake failed: ${failure}`);
  },
};

function post(env, headers, body) {
  return fetch(env.HELM_INTAKE_URL, { method: "POST", headers, body });
}

async function reason(response) {
  try {
    const payload = await response.json();
    const error = payload && payload.error;
    // A bare not_found means email is turned off in Helm (or this Worker is
    // stale); say so instead of bouncing "route not found".
    if (error && error.code === "not_found") return "Helm is not accepting email at this address";
    if (error && typeof error.message === "string" && error.message) return error.message.slice(0, 200);
  } catch {}
  return `Helm refused the message (HTTP ${response.status})`;
}

async function fallback(message, env, failure) {
  if (!env.FALLBACK_TO) return false;
  await message.forward(env.FALLBACK_TO, new Headers({ "X-Helm-Intake-Failed": failure }));
  return true;
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
