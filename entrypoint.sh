#!/bin/sh
set -e

# --- NEW: EULA gate -----------------------------------------------------------
# Must be checked before anything else — even before the root/non-root split.
if [ "$ACCEPT_EULA" != "yes" ]; then
    echo "[entrypoint] ERROR: You must accept the MakeMKV End User License Agreement."
    echo "[entrypoint] Read the EULA at: https://www.makemkv.com/eula/"
    echo "[entrypoint] Once accepted, set the environment variable: ACCEPT_EULA=yes"
    exit 1
fi
# ------------------------------------------------------------------------------

# --- NEW: MakeMKV install function --------------------------------------------
MAKEMKV_VERSION="${MAKEMKV_VERSION:-1.18.3}"
MAKEMKV_CACHE_DIR="/config/makemkv-${MAKEMKV_VERSION}"

install_makemkv() {
    if [ -x "${MAKEMKV_CACHE_DIR}/bin/makemkvcon" ]; then
        echo "[entrypoint] Restoring MakeMKV ${MAKEMKV_VERSION} from cache..."
        cp "${MAKEMKV_CACHE_DIR}/bin/makemkvcon" /usr/bin/makemkvcon
        cp "${MAKEMKV_CACHE_DIR}/lib/"*.so.* /usr/lib/
        if [ -d "${MAKEMKV_CACHE_DIR}/share/MakeMKV" ]; then
            cp -r "${MAKEMKV_CACHE_DIR}/share/MakeMKV" /usr/share/
        fi
        ldconfig
        echo "[entrypoint] MakeMKV ${MAKEMKV_VERSION} restored from cache."
        return
    fi

    echo "[entrypoint] Installing MakeMKV ${MAKEMKV_VERSION} (this may take several minutes)..."

    MKTMP=$(mktemp -d)

    wget -q -P "$MKTMP" \
        "https://www.makemkv.com/download/makemkv-oss-${MAKEMKV_VERSION}.tar.gz" \
        "https://www.makemkv.com/download/makemkv-bin-${MAKEMKV_VERSION}.tar.gz"

    tar xf "$MKTMP/makemkv-oss-${MAKEMKV_VERSION}.tar.gz" -C "$MKTMP"
    tar xf "$MKTMP/makemkv-bin-${MAKEMKV_VERSION}.tar.gz" -C "$MKTMP"

    # Build and install open-source libs (libdriveio, libmakemkv)
    cd "$MKTMP/makemkv-oss-${MAKEMKV_VERSION}"
    ./configure --disable-gui
    make
    make install

    # Accept EULA and install proprietary binary (makemkvcon, libmmbd)
    mkdir -p "$MKTMP/makemkv-bin-${MAKEMKV_VERSION}/tmp"
    echo "accepted" > "$MKTMP/makemkv-bin-${MAKEMKV_VERSION}/tmp/eula_accepted"
    cd "$MKTMP/makemkv-bin-${MAKEMKV_VERSION}"
    make install

    ldconfig

    # Cache installed artifacts to /config so subsequent starts skip compilation
    mkdir -p "${MAKEMKV_CACHE_DIR}/bin" "${MAKEMKV_CACHE_DIR}/lib" "${MAKEMKV_CACHE_DIR}/share"
    cp /usr/bin/makemkvcon "${MAKEMKV_CACHE_DIR}/bin/"
    for lib in libdriveio.so.0 libmakemkv.so.1 libmmbd.so.0; do
        [ -f "/usr/lib/$lib" ] && cp "/usr/lib/$lib" "${MAKEMKV_CACHE_DIR}/lib/"
    done
    [ -d /usr/share/MakeMKV ] && cp -r /usr/share/MakeMKV "${MAKEMKV_CACHE_DIR}/share/"

    # Cleanup build temp dir
    rm -rf "$MKTMP"
    cd /

    echo "[entrypoint] MakeMKV ${MAKEMKV_VERSION} installed and cached to ${MAKEMKV_CACHE_DIR}."
}
# ------------------------------------------------------------------------------

# Defaults matching Unraid's nobody:users
USER_ID="${USER_ID:-99}"
GROUP_ID="${GROUP_ID:-100}"
UMASK="${UMASK:-0000}"

# Apply umask
umask "$UMASK"

# If running as root (normal container startup), set up user and drop privileges.
# If already running as non-root (e.g., Kubernetes runAsUser), skip setup and exec directly.
if [ "$(id -u)" -eq 0 ]; then

    # --- NEW: Install MakeMKV before dropping privileges ----------------------
    install_makemkv
    # --------------------------------------------------------------------------

    # Create/modify group
    GROUP_NAME="bluforge"
    if getent group "$GROUP_ID" >/dev/null 2>&1; then
        GROUP_NAME=$(getent group "$GROUP_ID" | cut -d: -f1)
    else
        groupadd -g "$GROUP_ID" "$GROUP_NAME"
    fi

    # Create/modify user
    USER_NAME="bluforge"
    if id "$USER_ID" >/dev/null 2>&1; then
        USER_NAME=$(id -nu "$USER_ID")
        usermod -d /home/bluforge -s /bin/sh -g "$GROUP_ID" "$USER_NAME" 2>/dev/null || true
    else
        useradd -u "$USER_ID" -g "$GROUP_ID" -d /home/bluforge -s /bin/sh -M "$USER_NAME"
    fi

    # Add user to groups that own optical drive devices (/dev/sr*, /dev/sg*).
    # Different hosts use different groups (disk, cdrom, etc.) so we detect
    # the actual group owners from the devices present in the container.
    DRIVE_GROUPS=""
    for dev in /dev/sr* /dev/sg*; do
        [ -e "$dev" ] || continue
        DEV_GID=$(stat -c '%g' "$dev")
        # Skip root-owned devices (GID 0) — we'd need root for those anyway.
        [ "$DEV_GID" = "0" ] && continue
        # Ensure the group exists in the container.
        if ! getent group "$DEV_GID" >/dev/null 2>&1; then
            groupadd -g "$DEV_GID" "devgroup${DEV_GID}"
        fi
        GRP_NAME=$(getent group "$DEV_GID" | cut -d: -f1)
        # Collect unique group names.
        case ",$DRIVE_GROUPS," in
            *",$GRP_NAME,"*) ;;
            *) DRIVE_GROUPS="${DRIVE_GROUPS:+$DRIVE_GROUPS,}$GRP_NAME" ;;
        esac
    done
    if [ -n "$DRIVE_GROUPS" ]; then
        usermod -aG "$DRIVE_GROUPS" "$USER_NAME" 2>/dev/null || true
    fi

    # Ensure home directory exists and is owned correctly
    mkdir -p /home/bluforge
    chown "$USER_ID:$GROUP_ID" /home/bluforge

    # Ensure /config is writable by the configured user. Recursive chown is
    # safe here — /config holds only small config/db files, not media.
    chown -R "$USER_ID:$GROUP_ID" /config 2>/dev/null || true
    # Only chown the top-level /output directory, not recursively — it may
    # contain thousands of media files with intentional permissions.
    chown "$USER_ID:$GROUP_ID" /output 2>/dev/null || true

    # Set HOME so MakeMKV's setupMakeMKVData() finds ~/.MakeMKV correctly
    export HOME=/home/bluforge

    echo "[entrypoint] Starting BluForge as UID=$USER_ID GID=$GROUP_ID UMASK=$UMASK"

    # Drop privileges and exec the application. Use the username (not UID:GID)
    # so gosu resolves supplementary groups from /etc/group (e.g., disk group
    # for /dev/sr* access). Using UID:GID would clear supplementary groups.
    exec gosu "$USER_NAME" /app/bluforge "$@"
else
    # Already non-root (e.g., Kubernetes with runAsUser)
    echo "[entrypoint] Running as UID=$(id -u), skipping user setup"
    exec /app/bluforge "$@"
fi
