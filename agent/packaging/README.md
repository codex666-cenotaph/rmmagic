# Agent packaging

`.deb` / `.rpm` packages for the rmmagic endpoint agent, built with
[nfpm](https://nfpm.goreleaser.com/). This is the distributable install
path; for building from a source checkout use `deploy/install-agent.sh`.

## Layout

| File | Purpose |
|---|---|
| `nfpm.yaml` | Package definition (contents, metadata, maintainer scripts) |
| `rmmagent.service` | systemd unit installed to `/lib/systemd/system` |
| `scripts/postinstall.sh` | deb `postinst` / rpm `%post` — reload systemd, restart on upgrade |
| `scripts/preremove.sh` | deb `prerm` / rpm `%preun` — stop+disable on real removal |
| `scripts/postremove.sh` | deb `postrm` / rpm `%postun` — reload systemd, purge state on `purge` |
| `build.sh` | Cross-compile binaries + run nfpm per arch/format |

## Build

```sh
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest   # one-time
make agent-packages          # -> dist/*.deb, *.rpm (linux amd64+arm64) + windows .exe
make agent-binaries          # binaries only, no nfpm needed
```

Or directly, with overrides:

```sh
VERSION=0.4.0 agent/packaging/build.sh --targets "linux/amd64 linux/arm64 windows/amd64"
```

Windows targets produce a bare `rmmagent-windows-<arch>.exe` (static,
cross-compiled): deploy/packaging for Windows (service wrapper, MSI,
Authenticode signing) lands in later phases of the Windows agent plan.

The version defaults to `git describe` (leading `v` stripped); untagged
builds become a `0.0.0-<commit>` semver prerelease so nfpm is happy.

## Install / lifecycle

```sh
sudo apt install ./rmmagent_<version>_amd64.deb     # or: dnf install ./rmmagent-<version>.x86_64.rpm
sudo rmmagent enroll --server https://YOUR_SERVER --token rmme_...
sudo systemctl enable --now rmmagent
```

The package ships the binary to `/usr/bin/rmmagent`, the unit, and a
root-only `/var/lib/rmmagent` state dir. A fresh install does **not**
auto-start the service (the device isn't enrolled yet); an upgrade
restarts it only if it was already running. `apt purge` removes the state
directory (device identity); a plain remove keeps it so reinstalling
resumes the same identity.

## Signing (release only)

The base `nfpm.yaml` produces unsigned packages so local/dev builds need no
keys. The plan calls for GPG-signed deb/rpm at release; wire that into the
release pipeline by setting nfpm's `deb.signature.key_file` /
`rpm.signature.key_file` (and `*_KEY_ID`) via a release-only overlay config
that merges over this one, keeping signing keys out of the default build.
