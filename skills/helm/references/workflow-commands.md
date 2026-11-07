# Workflow commands

These commands use the shared Helm client and existing task lifecycle.


```sh
python3 scripts/helm.py renew --task TC-1 --operation-id UUIDV4
python3 scripts/helm.py release --task TC-1 --operation-id UUIDV4
python3 scripts/helm.py events --after 0 --project TC
python3 scripts/helm.py dependencies list --task TC-1
python3 scripts/helm.py dependencies add --task TC-1 --prerequisite TC-2
python3 scripts/helm.py timeline --task TC-1 --kind agent_progress
python3 scripts/helm.py timeline --project TC --before CURSOR
python3 scripts/helm.py issues --severity untriaged --resolution unresolved
python3 scripts/helm.py bug-report --project TC --title "Observed failure" \
  --actual-behavior "The operation exits unexpectedly"
python3 scripts/helm.py bug-triage --task TC-2 --severity s2
python3 scripts/helm.py bug-resolve --task TC-2 --resolution fixed
python3 scripts/helm.py bug-duplicate --task TC-2 --duplicate-of TC-3
python3 scripts/helm.py bug-reopen --task TC-2 --reason "The regression remains reproducible"
python3 scripts/helm.py notifications list --unread
python3 scripts/helm.py watches list --task TC-2
```

Workflow mutations generate one UUIDv4 operation ID per logical request when
omitted and print it in the JSON result. Supply that same ID to replay a lost
response safely. Notification and watch mutations require UUIDv4 operation
IDs. Earlier lifecycle, bug, and dependency commands also accept explicit
legacy operation IDs; those non-UUID values retain deterministic idempotency
keys for older automation, while malformed IDs are rejected before any network
mutation.
Multi-step commands derive a distinct UUIDv4 mutation key per endpoint from the
operation ID, request path, and body so the server's per-request key reservation
is respected without sacrificing deterministic replay.
