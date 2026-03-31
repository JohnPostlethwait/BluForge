#!/bin/sh
set -e

# Defaults matching Unraid's nobody:users
USER_ID="${USER_ID:-99}"
GROUP_ID="${GROUP_ID:-100}"
UMASK="${UMASK:-0000}"

# Apply umask
umask "$UMASK"

# If running as root (normal container startup), set up user and drop privileges.
# If already running as non-root (e.g., Kubernetes runAsUser), skip setup and exec directly.
if [ "$(id -u)" -eq 0 ]; then

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
    echo "[entrypoint] User groups: $(id "$USER_NAME")"
    echo "[entrypoint] Device permissions:"
    ls -la /dev/sr* /dev/sg* 2>/dev/null || echo "[entrypoint] No /dev/sr* or /dev/sg* devices found"

    # Drop privileges and exec the application. Use the username (not UID:GID)
    # so gosu resolves supplementary groups from /etc/group (e.g., disk group
    # for /dev/sr* access). Using UID:GID would clear supplementary groups.
    exec gosu "$USER_NAME" /app/bluforge "$@"
else
    # Already non-root (e.g., Kubernetes with runAsUser)
    echo "[entrypoint] Running as UID=$(id -u), skipping user setup"
    exec /app/bluforge "$@"
fi
