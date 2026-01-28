#!/bin/bash
set -e

# --- Failsafe and OS-specific Setup ---
OS=$(uname -s)
SOCKET_PATH=""

if [ "$OS" == "Linux" ]; then
    SOCKET_PATH="${XDG_RUNTIME_DIR}/podman/podman.sock"
elif [ "$OS" == "Darwin" ]; then
    echo "Error: macOS is not supported by this dev container configuration." >&2
    echo "This setup depends on Linux-specific features like host networking and user namespace mapping (--userns=keep-id)." >&2
    echo "It also assumes the standard Linux rootless Podman socket path (\${XDG_RUNTIME_DIR}/podman/podman.sock)." >&2
    echo "To add macOS support, this script would need to be modified to handle the Podman Machine VM." >&2
    exit 1
else
    echo "Error: Unsupported operating system '$OS'." >&2
    exit 1
fi

if [ -z "$SOCKET_PATH" ] || [ ! -S "$SOCKET_PATH" ]; then
    echo "Error: Podman socket not found or not running at the expected path: ${SOCKET_PATH}" >&2
    echo "For Fedora/RHEL, please ensure the Podman service is running." >&2
    echo "You can typically start it with: systemctl --user start podman.socket" >&2
    exit 1
fi

# Check required sysctl settings for inotify limits (skip if unable to read values)
REQUIRED_MAX_USER_WATCHES=1048576
REQUIRED_MAX_USER_INSTANCES=1024

CURRENT_MAX_USER_WATCHES=$(sysctl -n fs.inotify.max_user_watches 2>/dev/null || echo "")
CURRENT_MAX_USER_INSTANCES=$(sysctl -n fs.inotify.max_user_instances 2>/dev/null || echo "")

# Only check if we successfully read both values
if [ -n "$CURRENT_MAX_USER_WATCHES" ] && [ -n "$CURRENT_MAX_USER_INSTANCES" ]; then
    if [ "$CURRENT_MAX_USER_WATCHES" -lt "$REQUIRED_MAX_USER_WATCHES" ]; then
        echo "Error: fs.inotify.max_user_watches is set to $CURRENT_MAX_USER_WATCHES but needs to be at least $REQUIRED_MAX_USER_WATCHES" >&2
        echo "Please run: sudo sysctl fs.inotify.max_user_watches=$REQUIRED_MAX_USER_WATCHES" >&2
        echo "To make this persistent across reboots, add 'fs.inotify.max_user_watches=$REQUIRED_MAX_USER_WATCHES' to /etc/sysctl.conf" >&2
        exit 1
    fi

    if [ "$CURRENT_MAX_USER_INSTANCES" -lt "$REQUIRED_MAX_USER_INSTANCES" ]; then
        echo "Error: fs.inotify.max_user_instances is set to $CURRENT_MAX_USER_INSTANCES but needs to be at least $REQUIRED_MAX_USER_INSTANCES" >&2
        echo "Please run: sudo sysctl fs.inotify.max_user_instances=$REQUIRED_MAX_USER_INSTANCES" >&2
        echo "To make this persistent across reboots, add 'fs.inotify.max_user_instances=$REQUIRED_MAX_USER_INSTANCES' to /etc/sysctl.conf" >&2
        exit 1
    fi
else
    echo "Warning: Unable to verify inotify limits (sysctl read failed). If pods fail with 'too many open files', ensure:" >&2
    echo "  sudo sysctl fs.inotify.max_user_watches=$REQUIRED_MAX_USER_WATCHES" >&2
    echo "  sudo sysctl fs.inotify.max_user_instances=$REQUIRED_MAX_USER_INSTANCES" >&2
fi
# --- End Failsafe ---

# This script is called by `initializeCommand` in devcontainer.json.
# It populates a .devcontainer.env file with host-specific information,
# which is then used by the container.

ENV_FILE=".devcontainer/devcontainer.env"

# Clear the file to start fresh
> "$ENV_FILE"

# Write git config to the env file so it's available inside the container
echo "GIT_AUTHOR_NAME=$(git config --get user.name)" >> "$ENV_FILE"
echo "GIT_AUTHOR_EMAIL=$(git config --get user.email)" >> "$ENV_FILE"
echo "GIT_COMMITTER_NAME=$(git config --get user.name)" >> "$ENV_FILE"
echo "GIT_COMMITTER_EMAIL=$(git config --get user.email)" >> "$ENV_FILE" 
