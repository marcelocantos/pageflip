#!/usr/bin/env bash
# Copyright 2026 Marcelo Cantos
# SPDX-License-Identifier: Apache-2.0
#
# meetings-digest.sh — Generate a weekly meta-pattern digest from meeting
# artifact folders produced by meetcat. Invokes `claude -p` with a composed
# prompt and writes the result to <meetings-dir>/digests/week-<YYYY-MM-DD>.md.
#
# Usage: meetings-digest.sh <meetings-dir>
#
#   meetings-dir   Root directory where meetcat writes per-meeting artifact
#                  folders.  Each subfolder may contain:
#                    decisions.md, actions.md, open-questions.md,
#                    contradictions.md, transcript.jsonl

set -euo pipefail

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

log() { printf '%s\n' "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

usage() {
    printf 'Usage: %s <meetings-dir>\n' "$(basename "$0")" >&2
    exit 1
}

# ---------------------------------------------------------------------------
# Argument validation
# ---------------------------------------------------------------------------

[[ $# -eq 1 ]] || usage

MEETINGS_DIR="$1"

[[ -d "$MEETINGS_DIR" ]] || die "meetings directory does not exist: $MEETINGS_DIR"

# ---------------------------------------------------------------------------
# Discover meeting folders modified in the last 7 days
# ---------------------------------------------------------------------------

# Use -mtime -7 (portable across macOS and Linux: matches folders whose
# status changed fewer than 7*24 h ago).
mapfile -t RECENT_FOLDERS < <(
    find "$MEETINGS_DIR" -mindepth 1 -maxdepth 1 -type d -mtime -7 | sort
)

if [[ ${#RECENT_FOLDERS[@]} -eq 0 ]]; then
    log "No meeting folders modified in the last 7 days — nothing to digest."
    exit 0
fi

log "Found ${#RECENT_FOLDERS[@]} recent meeting folder(s)."

# ---------------------------------------------------------------------------
# Assemble prompt content
# ---------------------------------------------------------------------------

ARTIFACTS=""

for folder in "${RECENT_FOLDERS[@]}"; do
    meeting_name="$(basename "$folder")"
    ARTIFACTS+="## Meeting: ${meeting_name}"$'\n\n'

    for artifact in decisions.md actions.md open-questions.md contradictions.md; do
        artifact_path="${folder}/${artifact}"
        if [[ -f "$artifact_path" ]]; then
            content="$(cat "$artifact_path")"
            if [[ -n "$content" ]]; then
                ARTIFACTS+="### ${artifact}"$'\n\n'
                ARTIFACTS+="${content}"$'\n\n'
            fi
        fi
    done

    # Include speaker summary if transcript.jsonl is present
    transcript_path="${folder}/transcript.jsonl"
    if [[ -f "$transcript_path" ]]; then
        # Extract unique speaker IDs and utterance counts
        speaker_summary="$(
            python3 -c "
import sys, json, collections
counts = collections.Counter()
for line in open('${transcript_path}'):
    try:
        obj = json.loads(line)
        spk = obj.get('speaker_id') or obj.get('speaker') or ''
        if spk:
            counts[spk] += 1
    except Exception:
        pass
if counts:
    print('Speaker utterance counts:')
    for spk, n in sorted(counts.items()):
        print(f'  {spk}: {n}')
" 2>/dev/null || true
        )"
        if [[ -n "$speaker_summary" ]]; then
            ARTIFACTS+="### speaker_data"$'\n\n'
            ARTIFACTS+="${speaker_summary}"$'\n\n'
        fi
    fi
done

# ---------------------------------------------------------------------------
# System prompt
# ---------------------------------------------------------------------------

SYSTEM_PROMPT='You are a meeting analyst. You will receive a week'\''s worth of meeting artifacts — decisions, actions, open questions, contradictions, and optional speaker data. Produce a concise meta-pattern digest covering:

1. **Recurring themes** — topics or concerns that appeared across multiple meetings.
2. **Drifting or reversed decisions** — decisions that changed, were walked back, or appear inconsistent across meetings.
3. **Aging open questions** — questions that appeared in more than one meeting without resolution.
4. **Fresh contradictions worth escalating** — newly surfaced contradictions that deserve attention.
5. **Speaker-pattern observations** — if speaker data is available, note patterns such as dominance, absence, or cross-meeting participation shifts.

Be concise and actionable. Each section should be a tight bullet list. If a section has no findings, write "None this week." Produce the digest in Markdown.'

# ---------------------------------------------------------------------------
# Invoke claude -p
# ---------------------------------------------------------------------------

PROMPT="Here are this week's meeting artifacts:\n\n${ARTIFACTS}"

log "Invoking claude -p …"
DIGEST="$(printf '%b' "$PROMPT" | claude -p --system "$SYSTEM_PROMPT")" \
    || die "'claude -p' exited with a non-zero status"

# ---------------------------------------------------------------------------
# Write digest file
# ---------------------------------------------------------------------------

DIGEST_DIR="${MEETINGS_DIR}/digests"
mkdir -p "$DIGEST_DIR"

TODAY="$(date '+%Y-%m-%d')"
OUTPUT_FILE="${DIGEST_DIR}/week-${TODAY}.md"

{
    printf '# Weekly Meeting Digest — %s\n\n' "$TODAY"
    printf '%s\n' "$DIGEST"
} > "$OUTPUT_FILE"

log "Digest written to: ${OUTPUT_FILE}"
