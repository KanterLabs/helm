# Closed bootstrap, legacy access and runtime integration

Failure inventory written before this integration harness. These are DEV
synthetic journeys, not live setup or TC-336 artifact acceptance.

1. The bootstrap might activate instead of close admission. Prepare a verified
   synthetic backup and permanently closed authority; actual Helm must reject
   that marker before migrations, preserving DB bytes and inode.
2. An old process may retain access after the UID transition. Start two actual
   legacy-UID processes first: one holds a writable FD to synthetic SQLite,
   another maps it shared/read-write and closes its FD. They do not mutate the
   database. Prepare must close directories and refuse before any regular-file
   handover. Prove the read-only host snapshot identifies both, including the mapping alone after
   the descriptor holder stops. A clean status before both stop is a failure.
   An unknown snapshot can still contain positive access evidence and must
   block activation; require those matching records rather than relabeling an
   incomplete scan as complete. After both holders stop, require a complete
   no-access snapshot with no suppressed errors.
   A separate target-bound container with no protected FD, mapping or cwd
   must also block handover, covering potential in-flight path operations.
3. A simulated /proc tree may look complete. Run the observer with actual DEV
   host PID visibility and read-only proc access, no fabricated process tree.
   Missing or inaccessible observations must remain unknown; a clean-snapshot
   assertion fails if the host cannot actually be inspected. The snapshot is
   never described as a continuing fence.
4. Bootstrap replay might recreate a lock, reset state or lose user writes.
   The same saved plan must preserve lock inode, DB inode/bytes and closed
   marker. Lost authority must refuse rather than regenerate it. Source and
   backup validation use synthetic data with stable IDs, integrity and FKs.
5. A probe could touch real data or leak secrets. All paths are newly created
   fixtures; the common native launcher exclusively creates and pins a new
   root-private fixture, then removes only that fixture. Preparation and the
   snapshot run through existing DEV sudo in the actual host
   PID and mount namespaces, reads process metadata and the fixture only, and
   never reads environment, command-line or descriptor contents. No network/ports,
   credentials, arbitrary host commands,
   live targets or image deletion. Probe output is bounded metadata only.
6. Error cleanup could leave holders or root-owned fixtures. Record every owned
   container name, remove those containers only and report remaining owned
   containers. Native fixture cleanup verifies its original directory inode.
7. Root inside a container could mistake matching private init namespaces for
   the host. Preparation requires an independently installed host binding.
   Tests provision this only inside exclusively owned native DEV fixtures;
   no live host binding is installed or inferred by the production code.

`test/deployment-policy/bootstrap_runtime_e2e.py --binary PATH` records the
actual process observations and closed-state outcome. Kernel proof alone does
not establish a complete platform fence. Activation, immutable candidate and
recovery, config/TLS health, protected workflow wiring and fresh live backups
remain separate gates.
