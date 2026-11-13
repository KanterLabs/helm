"""HTML pages and synthetic repositories for test/e2e/fake-gitea (all invented)."""
import html

PAGE = """<!doctype html><html lang="en"><head><meta charset="utf-8"><title>{title} - Fake Gitea</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>body{{font:15px/1.5 system-ui,sans-serif;margin:0;background:#f4f5f7;color:#222}}
header{{background:#609926;color:#fff;padding:10px 20px;font-weight:600}}main{{max-width:640px;margin:24px auto;padding:0 16px}}
section{{background:#fff;border:1px solid #ddd;border-radius:6px;padding:16px 20px;margin-bottom:16px}}
label{{display:block;margin:10px 0 4px;font-weight:600}}input[type=text],textarea{{width:100%;box-sizing:border-box;padding:6px}}
button{{padding:8px 16px;margin-right:8px;font-size:15px}}code{{background:#eee;padding:2px 4px;overflow-wrap:anywhere}}</style>
</head><body><header>Fake Gitea {version} · signed in as helm-reader</header><main>{body}</main></body></html>"""


def repositories():
    items = []
    for n in range(1, 58):
        owner = "helm-reader" if n % 3 else "e2e-team"
        items.append({"id": 1000 + n, "full_name": f"{owner}/repo-{n:02d}", "name": f"repo-{n:02d}",
                      "private": n % 4 == 0, "archived": n % 10 == 0, "owner": {"login": owner}})
    return items


REPOS = repositories()


def applications(apps):
    rows = "".join(f"<li>{html.escape(app['name'])}: <code>{html.escape(cid)}</code></li>" for cid, app in apps.items())
    body = f"""<section><h1>Applications</h1><h2>Create a new OAuth2 Application</h2>
    <form method="post" action="/user/settings/applications/oauth2">
    <label for="application_name">Application Name</label><input id="application_name" name="application_name" type="text" required>
    <label for="redirect_uris">Redirect URIs (one per line)</label><textarea id="redirect_uris" name="redirect_uris" rows="3" required></textarea>
    <label><input type="checkbox" name="confidential_client" checked> Confidential Client</label>
    <p><button type="submit">Create Application</button></p></form></section>
    <section><h2>Manage OAuth2 Applications</h2><ul>{rows or '<li>None yet</li>'}</ul></section>"""
    return body


def created(client_id, uris, confidential):
    # A real forge shows a confidential client's secret once; Helm never takes it,
    # so the page names it without echoing it into browser evidence.
    secret = "<p>Client Secret: shown once on a real forge (Helm does not use it).</p>" if confidential else ""
    body = f"""<section><h1>Application created</h1><p data-client-id-line>Client ID: <code data-client-id>{client_id}</code></p>{secret}
    <p>Redirect URIs: {''.join(f'<code>{html.escape(u)}</code> ' for u in uris)}</p></section>"""
    return body


def consent(name, scope, redirect, consent):
    scopes = "".join(f"<li><code>{html.escape(s)}</code></li>" for s in scope.split())
    body = f"""<section><h1>Authorize Application</h1>
    <p><strong>{html.escape(name)}</strong> would like to access your account <strong>helm-reader</strong>.</p>
    <p>It asks for:</p><ul data-scopes>{scopes or '<li>Everything (no scope requested)</li>'}</ul>
    <p>You will be returned to <code>{html.escape(redirect)}</code>.</p>
    <form method="post" action="/login/oauth/grant"><input type="hidden" name="consent" value="{consent}">
    <button type="submit" name="granted" value="true">Approve</button><button type="submit" name="granted" value="false">Deny</button></form></section>"""
    return body
