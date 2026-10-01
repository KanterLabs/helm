# Release artifact failure cases

The release checks exercise the artifacts after a local HTTP download. Each
case is deliberately phrased as a user-visible failure so a green build does
not merely prove that a tarball was created.

| Case | Failure prevented | Evidence required |
| --- | --- | --- |
| R1 | An amd64 archive contains an arm64 executable, or vice versa. | ELF machine checks for both supported Linux targets. |
| R2 | A release binary was built with the checked-in fallback page or an empty embed tree. | Start the downloaded binary and fetch `/`; the production UI marker must be present. |
| R3 | A package installs files outside the reviewed native layout or gives the service a root-owned writable state path. | Package file lists, modes, owners, and service sandbox assertions. |
| R4 | A fresh package cannot start as the unprivileged service account. | Downloaded archive is installed into an isolated root, then `/healthz` and `/readyz` are checked. |
| R5 | Restart or upgrade loses a populated SQLite database. | The downloaded binary creates a project and task through the API; a stopped SQLite `.backup` passes integrity checks, and the same IDs/counts are observed after upgrade and rollback. |
| R6 | SELinux or a strict systemd sandbox prevents writes under the state directory. | Native `/var/lib/helm` paths are explicit in tmpfiles, sysusers, package metadata, and `ReadWritePaths`. |
| R7 | Ordinary package removal deletes application data or the configured environment. | Package payloads mark `/etc/helm/helm.env` as a config file, package hooks contain no state deletion, and the E2E state directory remains populated after immutable payload replacement/removal simulation. |
| R8 | The optional native companion is silently required at startup. | The server starts and serves health/UI endpoints with the companion path absent. |
| R9 | A downloaded package is for the wrong CPU architecture. | Debian and RPM metadata architectures agree with their filenames and ELF payload; each selected archive binary is executed natively or through the target QEMU runner. |
| R10 | Package upgrade scripts run destructive restore/reset behavior. | Package script bodies are absent or limited to service reload/enable actions; no state deletion is allowed. |

`ci/test-release-artifacts.sh` is the executable E2E harness for these cases.
It intentionally skips package installation when the host lacks the native
package manager or an emulator for the target architecture; the release CI
job must run the full matrix on native amd64 and arm64 builders.
