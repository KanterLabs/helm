# Optional native companions

The base Helm package contains the server and its systemd account/layout. A
native companion is an explicit feature of the release metadata and is never
a hard dependency of `helm.service`:

* `codex` enables Luna task assistance. It is installed separately at
  `/usr/lib/helm/codex` and is selected by `HELM_CODEX_BINARY`.
* `helm-beta-switchd` is the root-owned private beta switch broker. It is
  installed only by a deployment that has opted into the beta feature and
  supplies the matching release-controller contract. Its absence must leave
  the base service healthy.

The package builder must omit a companion unless its feature is requested and
must never run a package removal hook that deletes `/var/lib/helm`.
