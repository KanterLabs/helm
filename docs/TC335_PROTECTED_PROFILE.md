# Operator protected runtime profile

Failure inventory written before the renderer and its checks. This profile
supports an explicitly operated beta rollout; automatic CI deployment remains
paused until its independent witness/controller integration is accepted.

- A legacy image forced to UID70541 could bypass pathname ownership. Require
  `/usr/local/bin/helm-admission`, absent from historical images, as the literal
  entrypoint as well as the integrated executable admission check.
- An old queued profile still runs UID65532 with no capabilities and cannot
  open the70541 private directory. Existing descriptors/mappings and namespaces
  must be drained before preparation; the profile does not revoke them.
- Preserve the existing one-app Coolify service, private port/network, TLS,
  runtime environment file, data/config directory identities and health check.
  Add only the read-only authority DIRECTORY mount and protected identity.
- Never mount an authority file alone, add capabilities/privilege, enable public
  ingress, interpolate user/entrypoint/bind paths, or use a second application.
- Verify Coolify's parsed profile and actual Docker user, groups, capabilities,
  no-new-privileges, rootfs, exact image, mounts and lease after start. Rendering
  a profile alone does not prove those execution-time conditions.
- Inspect all existing and stopped Docker binds for file-only aliases or other
  profiles that could bypass the directory barrier. Unknown aliases block.
- Before any live mutation, require actual immutable candidate/recovery runtime
  acceptance, a verified fresh offline backup and root-native operator authority.

`deploy/coolify/protected_compose.py beta` derives the protected profile from
the unchanged existing private renderer and prints secret-free JSON. Production
is renderable for validation but is outside the requested beta rollout.

The focused journey compares all unchanged ingress/storage/authentication
fields and actually attempts to start an exact historical image under the
protected entrypoint, using a new empty disposable fixture. It must refuse
before Helm starts. No historical image or application data is removed.
