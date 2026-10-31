# Runtime admission failure inventory

This inventory precedes implementation and isolation tests. The package is one
bounded startup check, not a deployment controller or complete startup barrier.

Fail closed when protected data exists and any of these conditions occurs:

- Data, database, authority directory, marker, or permanent lock is missing,
  unreadable, the wrong file type, or a final-component symlink.
- Data/database ownership or exact permission bits differ from 70541:70541 and
  0700/0600; authority directory or files differ from root:70541 and 0750/0640.
- The authority filesystem is writable, or its read-only status is unavailable.
- Real/effective UID or GID differs from 70541, supplementary groups differ from
  the single group 70541, process status is incomplete/ambiguous, capabilities
  are nonzero, or NoNewPrivs is not 1.
- The permanent lock is held by another process, cannot be exclusively locked
  using a read-only descriptor, or its pathname identity changes while acquiring
  or validating admission. Closing a lease must allow reacquisition without
  replacing or deleting the permanent lock.
- The marker is empty, truncated, oversized, malformed JSON, contains duplicate
  or unknown fields, lacks any required field, or contains trailing JSON.
- Marker format/minimum authorization/phase/lane/role/image/release SHA/nonce,
  generation, device or inode values violate the v1 contract.
- Marker data or lock identities disagree with opened descriptors; binary hash
  disagrees with the actual executable bytes, or executable hashing fails.
- A required operating-system observation fails, including file metadata,
  process status, filesystem status, marker reads, or lock operations.

Public startup remains compatible only when the protected data path is absent.
Other failures at that path must not be mistaken for absence. Non-Linux builds
must reject protected data while allowing the absent-path public case.

Isolation fixtures exercise filesystem and process snapshots without exporting
any production override. Production paths and credential checks remain fixed.
Tests capture sanitized command output under `.artifacts/tc335/runtime-admission`.
They do not assert Docker image identity, host ancestry/alias exclusion, child
writer drainage, paired roles, controller signatures, or rollout readiness.
