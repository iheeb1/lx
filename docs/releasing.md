# Releasing lx

## Cutting a release (maintainers)

```sh
make dist           # dist/: 6 archives (.zip for windows), install.sh and SHA256SUMS, self-verified
make install-test   # host-only dist, install.sh over file:// (no network), corrupted-archive refusal
make nonet          # fails if a networking package is linked into lx (linux, darwin, windows)
git tag v0.2.0 && git push origin v0.2.0   # release.yml: full CI, make dist, provenance attestation, GitHub release
```

Archive names carry no version, so `releases/latest/download/<name>` always
works:

- A tag that contains `-` (for example `v0.3.0-rc1`) is published as a
  prerelease, so `releases/latest`, and therefore `install.sh`, stay on the
  last stable version.
- The release job refuses to publish if the binary doesn't report the tag, or
  if it was built from a modified tree.
- `make dist` needs `zip` for the Windows archives, and GNU tar or bsdtar.

## Installing a release

```sh
curl -fsSL https://github.com/iheeb1/lx/releases/latest/download/install.sh | sh
```

The script picks the archive for your system (macOS or Linux, amd64 or arm64)
and checks it against the release's `SHA256SUMS`. It then installs one static
binary to `~/.local/bin`:

- It never uses sudo, and it tells you when that directory isn't on your PATH.
- If the checksum doesn't match, or the binary doesn't run as lx, it installs
  nothing and leaves any existing lx untouched.
- Set `LX_INSTALL_DIR=/somewhere` to install elsewhere, or
  `LX_VERSION=v0.2.0` to pick a release.

To download and check a release yourself:

```sh
curl -fsSLO https://github.com/iheeb1/lx/releases/latest/download/lx_darwin_arm64.tar.gz
curl -fsSLO https://github.com/iheeb1/lx/releases/latest/download/SHA256SUMS
grep lx_darwin_arm64.tar.gz SHA256SUMS | shasum -a 256 -c
tar -xzf lx_darwin_arm64.tar.gz lx && mkdir -p ~/.local/bin && mv lx ~/.local/bin/
gh attestation verify lx_darwin_arm64.tar.gz --repo iheeb1/lx   # optional: proves this repo's release workflow built it
```

Windows (experimental, untested): download `lx_windows_amd64.zip` or
`lx_windows_arm64.zip`, check it against `SHA256SUMS`, and put `lx.exe` on your
PATH.

`install.sh` is tested against fake releases:

- corrupted archives;
- bad or missing `SHA256SUMS`;
- missing curl or hash tools;
- relative install directories;
- the `dash`, `bash`, `ksh` and busybox shells.

On every change, CI builds all six release archives, installs one with
`install.sh`, and fails if a networking package is linked into lx.
