"""Read-only, allowlisted metadata for the TC-171 hostname incident."""
import json
import os
import urllib.error
import urllib.request

ZONE = "1206ce4daa0fe3c4791f9df9069764f6"
ACCOUNT = "090ae73dce25f4eca9a53ee396fdc916"


def read(path):
    req = urllib.request.Request(
        "https://api.cloudflare.com/client/v4" + path,
        headers={"Authorization": "Bearer " + os.environ["CLOUDFLARE_API_TOKEN"]},
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            data = json.load(response)
    except urllib.error.HTTPError as error:
        print(json.dumps({"path": path, "http_status": error.code}))
        return []
    if not data.get("success"):
        raise RuntimeError("Cloudflare read failed; response suppressed")
    return data["result"]


def emit(label, records, fields):
    print(json.dumps({"kind": label, "records": [{k: r.get(k) for k in fields} for r in records]}))


for hostname in ("beta.shanekanterman.dev", "beta.tc.shanekanterman.dev"):
    emit("dns", read(f"/zones/{ZONE}/dns_records?name={hostname}"), ("id", "name", "type", "content", "proxied"))
emit("certificates", read(f"/zones/{ZONE}/ssl/certificate_packs"), ("id", "type", "hosts", "status"))
emit("access", [r for r in read(f"/accounts/{ACCOUNT}/access/apps") if "beta" in r.get("domain", "") or "beta" in r.get("name", "").lower()], ("id", "name", "domain", "type"))
emit("tunnels", [r for r in read(f"/accounts/{ACCOUNT}/cfd_tunnel?is_deleted=false") if any(x in r.get("name", "") for x in ("beta", "portfolio"))], ("id", "name", "status"))
