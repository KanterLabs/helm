# Helm releases

The supported source release is `v0.1.1`. It keeps the application schema and
authentication behavior intact while adding reproducible Linux release
artifacts.

Release metadata lives in [`packaging/release.json`](../packaging/release.json).
It is the single source for the version, supported `linux/amd64` and
`linux/arm64` targets, Debian/RPM architecture names, native state paths, and
optional companion features.

The release build has three kinds of output:

* `helm-<version>-linux-amd64.tar.gz` and
  `helm-<version>-linux-arm64.tar.gz` contain the statically linked server,
  systemd account/layout files, and no host-specific state.
* `helm_<version>_<arch>.deb` is suitable for Debian and Ubuntu.
* `helm-<version>-1.<arch>.rpm` is suitable for Fedora. The RPM builder passes
  an explicit target and host build architecture, so an x86_64 packager can
  assemble the aarch64 metadata without executing the target binary; the
  Fedora 43 matrix validates both package metadata and the ELF payload before
  publication.

All package formats install the service as the unprivileged `helm` account and
keep persistent state under `/var/lib/helm`. The service has a strict systemd
filesystem boundary with `/var/lib/helm` as its only writable path. Upgrades
replace the immutable executable and unit files; ordinary removal leaves the
database, Codex homes, and environment file in place. Package hooks only
create the service account/state directories and reload systemd metadata.

The Codex executable and the beta switch broker are optional native companion
features. A base package starts and serves health/UI endpoints when those
paths are absent; `HELM_LUNA_ENABLED=false` is the packaged default. Install a
companion and enable its setting only when that release feature is explicitly
enabled. OCI builds have a separate per-architecture helper stage; the build
must fail if that selected helper is for the wrong target.

For local work, use the Makefile release targets after the shared release tool
has been vendored:

```sh
make release-config-check
make release-archives RELEASE_VERSION=0.1.1
make release-packages RELEASE_VERSION=0.1.1 RELEASE_PLATFORM=linux/amd64 RELEASE_PACKAGE_FORMAT=deb
make release-packages RELEASE_VERSION=0.1.1 RELEASE_PLATFORM=linux/amd64 RELEASE_PACKAGE_FORMAT=rpm
make release-oci RELEASE_VERSION=0.1.1 OCI_PLATFORM=linux/amd64
make release-oci-multiarch RELEASE_VERSION=0.1.1
make release-e2e RELEASE_VERSION=0.1.1
```

`make release-e2e` downloads the generated artifacts through a local HTTP
server, checks ELF/package architecture and service metadata, starts each
requested architecture without its optional companion, creates a project and
task through the downloaded API, verifies a standalone SQLite backup, replaces
the executable and checks stable IDs/counts after restart, then rolls back to
the retained executable without restoring the database. Set
`RELEASE_TEST_ARCHES=amd64` or `arm64` on a native builder without the other
architecture's emulator; with QEMU installed, the default runs both. To test
compatibility with a real retained `v0.1.0` executable, set
`RELEASE_TEST_RETAINED_DIR` to a directory containing `<arch>/usr/bin/helm`
(or set `RELEASE_TEST_RETAINED_BINARY` for a single-architecture run). The
failure inventory is kept in [`ci/release-failure-cases.md`](../ci/release-failure-cases.md).

Do not use package removal or `docker compose down -v` as an upgrade step.
Those operations have different data-retention semantics; an explicit backup
and restore procedure belongs in the deployment runbook.
