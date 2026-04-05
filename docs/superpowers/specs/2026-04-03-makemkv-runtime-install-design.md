# Design: MakeMKV Runtime Installation

**Date:** 2026-04-03

## Problem

The current Dockerfile bakes the MakeMKV proprietary binary into the image at build time and programmatically accepts the EULA without user consent. This means anyone pulling a pre-built image receives MakeMKV redistributed without agreeing to the MakeMKV EULA, which conflicts with GuinpinSoft's license terms.

## Goal

Move MakeMKV installation to container startup so:
- No proprietary binaries are distributed in the image
- The user explicitly accepts the MakeMKV EULA before makemkvcon is installed
- The install is cached to avoid recompilation on every container restart

---

## Files Changed

- `Dockerfile`
- `entrypoint.sh`
- `docker-compose.yml`
- `docker-compose.unraid.yml`
- `README.md`

---

## Dockerfile

Remove the `makemkv-builder` stage entirely. The final runtime image gains the build tools previously only present in that stage:

- `build-essential`, `pkg-config`, `wget`
- `libssl-dev`, `libexpat1-dev`, `zlib1g-dev`
- `libavcodec-dev`, `libavutil-dev`, `libavformat-dev`

These are required to compile MakeMKV from source at runtime.

Add `MAKEMKV_VERSION` as a build `ARG` (default `1.18.3`) and also expose it as an `ENV` so the entrypoint can read it. Remove the `COPY --from=makemkv-builder` lines for `makemkvcon`, the `.so` files, and `/usr/share/MakeMKV`. Remove the `RUN ldconfig && ldd /usr/bin/makemkvcon` line.

---

## entrypoint.sh

Insert two new blocks at the top of the script, before any existing logic:

### Block 1 — EULA Gate

```sh
if [ "$ACCEPT_EULA" != "yes" ]; then
    echo "[entrypoint] ERROR: You must accept the MakeMKV End User License Agreement to use BluForge."
    echo "[entrypoint] Read the EULA at: https://www.makemkv.com/eula/"
    echo "[entrypoint] Once accepted, set the environment variable: ACCEPT_EULA=yes"
    exit 1
fi
```

This runs unconditionally — even before the root check — so both root and non-root container starts are gated.

### Block 2 — MakeMKV Install (root only)

Runs inside the existing `if [ "$(id -u)" -eq 0 ]` block, before user/group setup.

```
CACHE_DIR="/config/makemkv-${MAKEMKV_VERSION}"

if [ -x "${CACHE_DIR}/bin/makemkvcon" ]; then
    # Cache hit: restore binaries to system paths
    cp "${CACHE_DIR}/bin/makemkvcon" /usr/bin/makemkvcon
    cp "${CACHE_DIR}/lib/"*.so.* /usr/lib/
    [ -d "${CACHE_DIR}/share/MakeMKV" ] && cp -r "${CACHE_DIR}/share/MakeMKV" /usr/share/
    ldconfig
else
    # Cache miss: download, build, install, then cache
    1. Download makemkv-oss-${MAKEMKV_VERSION}.tar.gz and makemkv-bin-${MAKEMKV_VERSION}.tar.gz to a temp dir
    2. Build and install makemkv-oss (./configure --disable-gui && make && make install)
    3. Accept EULA file and install makemkv-bin (echo "accepted" > tmp/eula_accepted && make install)
    4. ldconfig
    5. Copy installed artifacts to ${CACHE_DIR}/bin/, ${CACHE_DIR}/lib/, ${CACHE_DIR}/share/
    6. Clean up temp build dir
fi
```

The cache is versioned by `MAKEMKV_VERSION`. Changing the version (via env var or image rebuild) triggers a fresh install automatically.

The EULA acceptance file (`tmp/eula_accepted`) written during install represents the user's consent via the `ACCEPT_EULA=yes` env var they set — the user has explicitly acknowledged the EULA to get this far.

---

## docker-compose.yml and docker-compose.unraid.yml

Add to the `environment` section of both files:

```yaml
- ACCEPT_EULA=yes           # Required: confirms acceptance of MakeMKV EULA at https://www.makemkv.com/eula/
- MAKEMKV_VERSION=1.18.3    # MakeMKV version to install. Tested: 1.18.3
```

---

## README.md

### Quick Start docker-compose example

Add `ACCEPT_EULA` and `MAKEMKV_VERSION` to the inline example, matching the compose files.

### Configuration table

Add two new rows:

| Setting | Env Var | Default | Description |
|---------|---------|---------|-------------|
| *(required)* | `ACCEPT_EULA` | *(none)* | Must be `yes` to accept the [MakeMKV EULA](https://www.makemkv.com/eula/) and enable the application |
| *(install)* | `MAKEMKV_VERSION` | `1.18.3` | MakeMKV version to download and install at startup. Tested version: `1.18.3` |

### License & Third-Party Software section

Replace the current statement "It does **not** bundle or distribute MakeMKV" with an accurate description of the runtime install model:

> MakeMKV is not bundled in the BluForge image. Instead, it is downloaded from [makemkv.com](https://www.makemkv.com/) and compiled at first container startup. Setting `ACCEPT_EULA=yes` confirms that you have read and accepted the [MakeMKV EULA](https://www.makemkv.com/eula/). The compiled binaries are cached to your `/config` volume to avoid recompilation on subsequent starts.

---

## Supported MakeMKV Versions

Only `1.18.3` has been tested. Other versions may work but are untested.

---

## Error Handling

- If `ACCEPT_EULA` is not `yes`: print clear message with EULA URL, exit 1
- If download fails (no network, makemkv.com unreachable): wget will fail, `set -e` in the entrypoint will propagate the error and the container will exit with a non-zero code
- If compilation fails: same — `set -e` propagates, container exits
- Cache is only written after a successful install, so a failed first-run leaves no partial cache

---

## Non-Goals

- Supporting interactive EULA acceptance
- Supporting pre-downloaded tarballs (users must have internet access on first start)
- Pinning or verifying tarball checksums (out of scope for this change)
- Installing MakeMKV in the non-root (Kubernetes `runAsUser`) path — that path skips user setup entirely and is already documented as requiring the user to handle the environment themselves
