#!/usr/bin/env bash
# Copyright 2026 Marcelo Cantos
# SPDX-License-Identifier: Apache-2.0
#
# install-digest-cron.sh — Install or uninstall a weekly launchd job that runs
# meetings-digest.sh every Sunday at 09:00 local time.
#
# Usage:
#   install-digest-cron.sh --meetings-dir DIR   Install the launchd plist
#   install-digest-cron.sh --uninstall          Unload and remove the plist

set -euo pipefail

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

PLIST_LABEL="com.marcelocantos.pageflip-digest"
PLIST_PATH="${HOME}/Library/LaunchAgents/${PLIST_LABEL}.plist"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIGEST_SCRIPT="${SCRIPT_DIR}/meetings-digest.sh"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

log() { printf '%s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
    cat >&2 <<EOF
Usage:
  $(basename "$0") --meetings-dir DIR   Install weekly digest launchd job
  $(basename "$0") --uninstall          Unload and remove launchd job

Options:
  --meetings-dir DIR    Path to the meetcat artifact root directory (required for install)
  --uninstall           Remove the launchd plist and unload the job
  -h, --help            Show this help text
EOF
    exit 1
}

unload_if_loaded() {
    if launchctl list "$PLIST_LABEL" &>/dev/null; then
        launchctl unload "$PLIST_PATH" 2>/dev/null || true
        log "Unloaded launchd job: ${PLIST_LABEL}"
    fi
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

ACTION=""
MEETINGS_DIR=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --meetings-dir)
            [[ $# -ge 2 ]] || die "--meetings-dir requires a value"
            MEETINGS_DIR="$2"
            ACTION="install"
            shift 2
            ;;
        --uninstall)
            ACTION="uninstall"
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            die "Unknown argument: $1"
            ;;
    esac
done

[[ -n "$ACTION" ]] || usage

# ---------------------------------------------------------------------------
# macOS guard
# ---------------------------------------------------------------------------

[[ "$(uname)" == "Darwin" ]] || die "This script only supports macOS (launchd). For Linux, add a crontab entry manually: 0 9 * * 0 ${DIGEST_SCRIPT} <meetings-dir>"

# ---------------------------------------------------------------------------
# Uninstall
# ---------------------------------------------------------------------------

if [[ "$ACTION" == "uninstall" ]]; then
    if [[ ! -f "$PLIST_PATH" ]]; then
        log "No plist found at ${PLIST_PATH} — nothing to uninstall."
        exit 0
    fi
    unload_if_loaded
    rm -f "$PLIST_PATH"
    log "Removed plist: ${PLIST_PATH}"
    exit 0
fi

# ---------------------------------------------------------------------------
# Install
# ---------------------------------------------------------------------------

[[ -n "$MEETINGS_DIR" ]] || die "--meetings-dir is required for install"
[[ -d "$MEETINGS_DIR" ]] || die "meetings directory does not exist: $MEETINGS_DIR"
[[ -x "$DIGEST_SCRIPT" ]] || die "digest script not found or not executable: $DIGEST_SCRIPT"

# Resolve absolute path
MEETINGS_DIR="$(cd "$MEETINGS_DIR" && pwd)"

LOG_DIR="${HOME}/Library/Logs/pageflip"
mkdir -p "$LOG_DIR"

LAUNCH_AGENTS_DIR="${HOME}/Library/LaunchAgents"
mkdir -p "$LAUNCH_AGENTS_DIR"

# Unload existing job if present
unload_if_loaded

cat > "$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
    "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${PLIST_LABEL}</string>

    <key>ProgramArguments</key>
    <array>
        <string>${DIGEST_SCRIPT}</string>
        <string>${MEETINGS_DIR}</string>
    </array>

    <!-- Every Sunday at 09:00 local time -->
    <key>StartCalendarInterval</key>
    <dict>
        <key>Weekday</key>
        <integer>0</integer>
        <key>Hour</key>
        <integer>9</integer>
        <key>Minute</key>
        <integer>0</integer>
    </dict>

    <key>StandardOutPath</key>
    <string>${LOG_DIR}/digest-stdout.log</string>
    <key>StandardErrorPath</key>
    <string>${LOG_DIR}/digest-stderr.log</string>

    <key>RunAtLoad</key>
    <false/>
</dict>
</plist>
PLIST

launchctl load "$PLIST_PATH"
log "Installed and loaded launchd job: ${PLIST_LABEL}"
log "Plist: ${PLIST_PATH}"
log "Logs:  ${LOG_DIR}/"
log "Schedule: every Sunday at 09:00"
log ""
log "To trigger immediately for testing:"
log "  launchctl start ${PLIST_LABEL}"
log ""
log "To uninstall:"
log "  $(basename "$0") --uninstall"
