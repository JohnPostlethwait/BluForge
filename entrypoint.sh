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

    # Add user to disk group for /dev/sr* and /dev/sg* access
    usermod -aG disk "$USER_NAME" 2>/dev/null || true

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
