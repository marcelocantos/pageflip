# Contributing to pageflip

Thanks for your interest. A few ground rules before you open a PR.

## Code of conduct

Be excellent to each other. This project's use case is confidential corporate
meetings, so discussions should stay on technical substance — examples and
repro steps should be generic.

## The audio invariant — read this before doing anything with audio

pageflip has a hard legal/policy constraint, enforced architecturally:

- Captured audio may be **used** to derive transcripts in real time.
- Captured audio bytes must **never** be written to disk (no WAV, no raw, no
  temp files, no cache).
- Captured audio bytes must **never** be transmitted over the network (no
  SaaS transcription, no cloud inference, no logging of audio bytes).
- Transcripts — text derived from local Whisper inference — are **fine** to
  persist or send over the wire; they are derived text, not audio.

This is enforced by keeping `AudioSamples` crate-private in `src/audio/`
with no Serialize / file-write / network trait implementations. The only
thing that leaves the audio module is a `Segment` (transcript text).

If your change might weaken this boundary, stop and open an issue using
the feature-request template.

## Build

```bash
make bullseye
```

…runs fmt, clippy, release build, and release tests. CI runs the same on
every push and PR. Keep it green locally before pushing.

The `test` target uses `--release` deliberately: the debug build's rpath
doesn't pick up `/usr/lib/swift`, which screencapturekit needs. The
`build.rs` at the repo root patches the release rpath so release tests
link cleanly.

## Model weights

Some features (T9.2 transcription, T9.3 diarisation) need HuggingFace
model weights. Fetch them once:

```bash
# Install the HF CLI (one-time)
uv tool install 'huggingface_hub[cli]'
hf auth login    # paste a read-token from https://huggingface.co/settings/tokens

# Accept the licence at each of these URLs first, then fetch:
hf download pyannote/segmentation-3.0
hf download pyannote/speaker-diarization-3.1
hf download pyannote/speaker-diarization-community-1
hf download pyannote/wespeaker-voxceleb-resnet34-LM
hf download Systran/faster-whisper-large-v3
hf download jonatasgrosman/wav2vec2-large-xlsr-53-english
```

Weights land in `~/.cache/huggingface/hub/`. Runtime uses
`HF_HUB_OFFLINE=1` — no network needed after bootstrap.

## Pull requests

- Squash-merge only. The PR title becomes the commit message on master.
- Keep PRs focused on a single target where practical.
- If your PR changes a `bullseye.yaml` target's acceptance criteria, explain
  why in the PR body and link the relevant issue.
- Run `make bullseye` before pushing; CI runs the same, and a red build
  blocks the merge button.

## Filing bugs

Use the [Bug report](/.github/ISSUE_TEMPLATE/bug_report.yml) template. Paste
the output of `pageflip doctor` and (if applicable) `meetcat doctor` — these
commands emit a sensitivity-safe markdown report containing versions,
permission states, model cache status, and counts. They do **not** include
meeting content, window titles, OCR, or transcript text.

See [`docs/bug-report.md`](docs/bug-report.md) for the full flow.

## Target graph

Ongoing work is tracked in `bullseye.yaml` (rendered view in `docs/targets.md`
if present). Each target is a desired end-state with testable acceptance
criteria. Adding a new capability typically means adding a new target rather
than piling into an existing one.
