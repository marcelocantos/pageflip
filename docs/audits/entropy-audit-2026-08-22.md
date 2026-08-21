# Entropy audit — pageflip

Date: 2026-08-22
Mode: full (entropy + explicit hygiene validation)
Auditor: entropy-audit owner (single-repo campaign)

## Executive summary

- **Snapshot:** `/Users/marcelo/work/github.com/marcelocantos/pageflip`
- **Branch:** `push/rename-binaries-pageflip-is-the-orchestrator-pagef` (tracks `origin/master`, ahead 1)
- **HEAD:** `5b77f1045eb144acf25986f8b5f0505796a15cc5` — `feat(T33): rename binaries — pageflip is the orchestrator, pageflip-capture is the engine`
- **Initial dirty state:** clean. `git status --porcelain=v1 -b` showed only the branch header; no staged, unstaged, or untracked files.
- **Tags:** `v0.1.0`, `v0.2.0`. This snapshot is unreleased rename work on top of v0.2.0.
- **hygiene.yaml:** absent.

**Headline mechanism.** T33 renamed the user-facing binary from `meetcat` (Go) to `pageflip` and the capture engine from `pageflip` to `pageflip-capture`, but the repository still carries three overlapping identities (meetcat directory + docs, Rust crate still named `pageflip`, Go module still `…/meetcat`). That incomplete split multiplies every subsequent change: protocol fields, doctor output, Homebrew packaging, bullseye targets, and help text all have more than one owner. Independently, the standing `make bullseye` Go half is red on this snapshot because `meetcat/go.mod` replaces claudia with a sibling checkout that CI cannot see.

**Highest-consequence findings.**

1. `make go-build` / `make go-test` fail here (parent `go.work` + `replace => ../../claudia`). GitHub Actions would fail the same recipes for a different reason: the replacement directory is not in the pageflip checkout.
2. T33 is implemented in binaries and `release.yml` but not in STABILITY.md, bug-report docs, issue templates, the web UI title, `packaging/pageflip.rb`, or bullseye (🎯T32 still ships a `meetcat` binary; 🎯T33 is not a target).
3. 🎯T10.3 is `achieved` as a type-level egress gate; `RedactPipeline::apply_and_seal` does not exist, `RedactedFrame` is `dead_code`, and `AppAllowlist` is never consulted on the capture path.
4. The slide-event NDJSON contract has four partial owners and no schema validator, while the producer hard-codes optional enrichment fields to `None`.

**Unverified residue.** Live capture freshness (🎯T24), WhisperX/pyannote temp-file behaviour, whether `uv run --with` hits the network at transcription time, and whether the Homebrew tap still installs pre-rename names. Those need a machine with Screen Recording permission, model weights, and tap access.

## Scope and exclusions

**In scope.** Tracked source and config: Rust capture crate (`src/`, `build.rs`, `Cargo.toml`), Go orchestrator (`meetcat/`), web UI (`meetcat/web/`), scripts, CI/release workflows, packaging template, docs, `bullseye.yaml`, Makefile.

**Named exclusions (not silent omissions).**

| Path | Role | Treatment |
|---|---|---|
| `target/` | Cargo build cache (gitignored) | Excluded. Binaries observed only as evidence that a prior local build exists. |
| `pageflip-*/`, `pageflip-capture-*/`, `meetcat-*/` | Per-run capture and meeting artefacts (gitignored) | Named session residue; not production source. |
| `meetcat/pageflip` | Local Go build artefact (gitignored) | Excluded. |
| Vendored crates in `Cargo.lock` / `go.sum` | Lockfiles | Inspected for replace/path issues, not reviewed crate-by-crate. |
| Sibling `../../claudia` | Out of repo | Cited only as the `replace` target that this module depends on. |

Languages analysed: Rust (primary crate), Go (orchestrator + embedded web), Python (`scripts/transcribe.py`), Bash (`scripts/*.sh`), HTML/JS/CSS (`meetcat/web/`). SQL/C++/TLA+ are absent.

## Commands run

| Command | Version / notes | Exit | Shipped vs auxiliary | Limitations |
|---|---|---|---|---|
| `git rev-parse HEAD`; `git status --porcelain=v1 -b`; `git log -1` | git 2.55.0 | 0 | provenance | Working tree clean at start. |
| `rustc --version`; `cargo --version` | rustc/cargo 1.96.0 (Homebrew) | 0 | env | Crate pins `rust-version = "1.75"`; no `rust-toolchain.toml`. |
| `go version` | go 1.26.4 darwin/arm64 | 0 | env | Module declares `go 1.26.1`. |
| `cargo fmt --check` | cargo 1.96.0 | 0 | shipped (`make fmt`) | Formatting only. |
| `cargo test --bin pageflip-capture --quiet` | 106 passed, 0.67s, debug | 0 | auxiliary (Makefile uses `--release`) | Does not exercise ScreenCaptureKit, `/usr/sbin/screencapture`, or WhisperX. CONTRIBUTING.md claims debug rpath fails for SCK; these unit tests do not load that path. |
| `cargo test --bin pageflip-feed --quiet` | 5 passed | 0 | auxiliary | Feeder unit tests only. |
| `make go-test` | Makefile recipe `cd meetcat && go test ./...` | 2 | shipped | Fails: `directory prefix . does not contain modules listed in go.work`. Parent `/Users/marcelo/work/github.com/marcelocantos/go.work` lists `./claudia` and `./jevons` only. |
| `make go-build` | `cd meetcat && go build -o pageflip .` | 2 | shipped | Same `go.work` rejection. |
| `GOWORK=off go test ./...` (in `meetcat/`) | go 1.26.4 | 1 | diagnostic | Hits `replace => ../../claudia`; missing `go.sum` entries for claudia's AWS Bedrock and websocket imports. |
| `~/.claude/skills/hygiene/hygiene_check.py` | uv-run skill script | 1 | hygiene skill | `FileNotFoundError: …/hygiene.yaml`. Posture undeclared; validator not initialised. |
| `make bullseye` / `cargo clippy --release` / `cargo test --release` | not run | — | shipped gate | Go half already red; release clippy/test would recompile with `lto=thin`. Residue: clippy and release-profile tests not re-executed this pass. |
| Clone detector / cargo-machete / cargo-deny | not installed | — | — | Not added. Duplication inspected by reading paired files. |

## Observed architecture

### Deployable units

```
pageflip (Go, meetcat/)  --spawns-->  pageflip-capture (Rust, src/main.rs)
        |                                    |
        | NDJSON slide events on stdout      | PNG files under pageflip-capture-<ts>/
        v                                    v
   localhost:49831 web UI              optional WhisperX (uv + scripts/transcribe.py)
   claudia specialist sessions
   meeting.json / session-ids.json

pageflip-feed (Rust, src/bin/pageflip-feed.rs)  -- leftover T5 consumer of PNG watch / NDJSON
```

Declared (README after T33): users invoke `pageflip`; it owns the web UI, specialist pool, and manifest; it spawns `pageflip-capture` for region/window capture, RMS dedup, and the event stream.

Observed:

- Cargo package name remains `pageflip`; binaries are `pageflip-capture` and `pageflip-feed` (`Cargo.toml:14-20`).
- Go module path remains `github.com/marcelocantos/pageflip/meetcat` (`meetcat/go.mod:1`); source lives in `meetcat/`; Makefile emits `meetcat/pageflip` (`Makefile:18-19`).
- Capture: region path shells out to `/usr/sbin/screencapture` writing a tempfile PNG (`src/capture/macos.rs:136-173`); window path still uses xcap's `capture_image` (`src/capture/macos.rs:61-71`). Audio still uses the `screencapturekit` crate (`src/audio/capture.rs:14`).
- Orchestrator: `spawnCapture` execs `pageflip-capture --events-out stdout` (`meetcat/spawn.go:36-37`). Web server binds `127.0.0.1:49831` (`meetcat/web.go:476-477`). Specialists are a `SessionPool` with per-role queues (`meetcat/agents.go:544-554`).
- Policy/redaction modules exist in Rust but the live save path is `pipeline.apply(frame)` then `Frame::save_png` (`src/main.rs:561-567`), not a sealed egress type.

### Dependency direction

```
meetcat/main.go --> spawn.go --> exec pageflip-capture
                --> agents.go --> github.com/marcelocantos/claudia (replaced with ../../claudia)
                --> web.go    --> embed meetcat/web/*
                --> revisits.go (RMS over saved PNGs)
src/main.rs     --> capture, dedup, redact, policy, events, audio, picker, doctor, session_log
pageflip-feed   --> notify + claude CLI (no import of the capture crate; separate bin)
```

No compile-time cycle between Go and Rust (process boundary). Semantic cycle: both implement RMS, both implement doctor, both define a slide-event struct, both write session logs.

### Declared vs observed rules

| Rule | Status |
|---|---|
| User-facing binary is `pageflip`; capture engine is `pageflip-capture` | Observed in Cargo `[[bin]]`, Makefile, `release.yml`, README. Contradicted by STABILITY.md, bug-report docs, issue templates, web `<title>`, 🎯T32. |
| Web UI binds 127.0.0.1 only | Observed and matches STABILITY.md. |
| Raw audio never leaves `src/audio/` | Type privacy holds for `AudioSamples`. WhisperX subprocess receives PCM on stdin (`src/audio/whisperx.rs:226-237`). Disk write of audio not observed in first-party code. |
| Slide events validated against `docs/slide-event-schema.json` (🎯T18) | Contradicted. Go `slideEvent.validate()` is field checks only (`meetcat/main.go:105-116`). No schema library. |
| Type-level redaction-before-egress (🎯T10.3) | Contradicted. See ENT-003. |
| `make bullseye` is the merge gate | Declared in Makefile, CI, CONTRIBUTING. Go recipes fail on this snapshot (ENT-001). |

## Dimension vector

| Dimension | State | Evidence summary | Change from baseline |
|---|---|---|---|
| Architecture topology | concern | Clean process split (Go orchestrator / Rust engine) undercut by incomplete rename and a capture-backend fork (xcap vs `screencapture`). | no prior entropy report |
| Redundancy / sources of truth | concern | Names, event schema, RMS, prompts, doctor, versions, Homebrew formula vs `release.yml`. | n/a |
| Change amplification | concern | T33 already required coordinated edits across Cargo, Go, CI, docs; leftovers remain in the highest-churn files (`bullseye.yaml` 27 commits, `src/main.rs` 18, `src/help_agent.txt` 9). | n/a |
| Local code quality | concern | Rust units are linear and tested; Go retains bubbletea-era comments; several `dead_code` policy types; `help_agent.txt` disagrees with clap. | n/a |
| Correctness / verification | concern | 111 Rust tests green. Go tests unrunnable here. No web/httptest/Playwright. Achieved T10.3 and T12 do not hold on the shipped path. | n/a |
| Security / dependencies | concern | Localhost UI + `html.WithUnsafe`; claudia path replace; no vuln scanner declared; audio invariant is type-level for first-party code only. | n/a |
| Build / release / operations | critical | Standing Go recipes exit 2. `replace ../../claudia` is non-hermetic. `packaging/pageflip.rb` still v0.1.0 with pre-rename install lines. | n/a |
| Documentation / governance | concern | STABILITY.md, bug-report flow, 🎯T32, missing 🎯T33/T34, no `hygiene.yaml`, no `agents-guide.md` (listed as a 1.0 gap). | n/a |

## Findings

### ENT-001: Go half of `make bullseye` is not hermetic

- **Priority:** P1
- **Dimensions:** Build / release / operations; Security / dependencies
- **Status:** observed fact
- **Evidence:**
  - `meetcat/go.mod:49` — `replace github.com/marcelocantos/claudia => ../../claudia`
  - `Makefile:18-22` — `go-build` / `go-test` `cd meetcat` without `GOWORK=off`
  - Parent `/Users/marcelo/work/github.com/marcelocantos/go.work` uses only `./claudia` and `./jevons`
  - `make go-test` exit 2: `pattern ./...: directory prefix . does not contain modules listed in go.work`
  - `GOWORK=off go test ./...` then fails: missing `go.sum` entries for `github.com/aws/aws-sdk-go-v2/…` and `github.com/coder/websocket` imported by sibling claudia (`bedrock.go`, `grok_acp.go`)
  - `.github/workflows/ci.yml:37-38` and `.github/workflows/release.yml:38-39,70-74` run `make bullseye` / `go build` inside a pageflip-only checkout; `../../claudia` is not present there
- **Mechanism:** The orchestrator no longer builds from the lockfile. Locally, Go walks up to a sibling `go.work` that does not include this module. In CI, the replace directory does not exist. Either way the declared gate cannot decide Go correctness.
- **Blast radius:** CI on this branch, the next tag (`release.yml` packages `target/release/pageflip` from the Go build), and any agent that treats `make bullseye` as green.
- **Counterevidence checked:** T33 commit message claims the Go build is green after the claudia v1 API rename. That was true only against a matching local claudia plus a workspace that included this module. `go.sum` still records `claudia v0.6.0`; the replace bypasses it.
- **Smallest coherent remediation:** Drop the path replace once a published claudia version exports the v1 API; pin that version in `go.mod`/`go.sum`. Set `GOWORK=off` in Makefile Go recipes so a parent workspace cannot hijack `cd meetcat`. Add `go work use ./pageflip/meetcat` only if this repo is intentionally part of the sibling workspace — do not do both.
- **Verification:** `GOWORK=off go test ./...` inside `meetcat/` on a clean checkout without `../claudia`; CI job on this branch.
- **Ratchet candidate:** Makefile `go-build`/`go-test` recipes export `GOWORK=off`; CI step `test ! -e meetcat/go.mod || ! grep -q 'replace .* => \.\./' meetcat/go.mod`.

### ENT-002: T33 renamed binaries; the product identity did not converge

- **Priority:** P1
- **Dimensions:** Architecture topology; Redundancy / sources of truth; Documentation / governance; Change amplification
- **Status:** observed fact
- **Evidence:**
  - README (`README.md:93-116`) and `.github/workflows/release.yml:70-81,115-128` agree: `pageflip` + `pageflip-capture` + `pageflip-feed`
  - `STABILITY.md:19-21` still lists `pageflip` as the capture loop and `meetcat` as the primary user-facing product
  - `docs/bug-report.md:10-18,39-45` still documents `pageflip doctor` auto-invoking `meetcat doctor` / `--no-meetcat`
  - `.github/ISSUE_TEMPLATE/bug_report.yml:51-57` still has a `meetcat doctor` field
  - `meetcat/web/index.html:5,11` — `<title>meetcat</title>`, default meeting-id `meetcat`
  - `src/doctor.rs:97` and `meetcat/doctor.go:21` both print `# pageflip doctor`
  - `packaging/pageflip.rb:10-39` still version `0.1.0`, installs `pageflip` + `pageflip-feed` only
  - 🎯T32 (`bullseye.yaml:617-625`) still requires a Homebrew `meetcat` binary
  - README cites 🎯T34 for retiring `pageflip-feed` (`README.md:114-116`); `bullseye.yaml` has no T33 or T34
- **Mechanism:** Operators, issue templates, and future release automation will follow whichever document they hit. After T33, `pageflip doctor` is the Go orchestrator, not the capture engine; `pageflip-capture doctor` optionally shells out to `pageflip doctor` (`src/doctor.rs:32-34,41-57`). The bug-report flow is inverted relative to the binaries.
- **Blast radius:** Bug reports, brew formula vs `release.yml` homebrew-releaser block, 🎯T32 work that would reintroduce a `meetcat` name, UI chrome.
- **Counterevidence checked:** `.gitignore` and Makefile *were* updated. `release.yml` install/test lines *were* updated. That makes the leftover docs/targets more dangerous, not less: the live shipping path and the catalogue disagree.
- **Smallest coherent remediation:** One identity pass: STABILITY binaries table, bug-report.md, issue templates, web title, doctor headings (`# pageflip-capture doctor` vs `# pageflip doctor`), `packaging/pageflip.rb` regenerated from `release.yml`, 🎯T32 rewritten or closed, 🎯T33 recorded as achieved on the commit that renamed the binaries.
- **Verification:** `rg -n '\\bmeetcat\\b' --glob '!bullseye.yaml' --glob '!docs/audit*' --glob '!docs/audits/**'` drops to an allowlist of historical audit-log entries and the `meetcat/` directory name (directory rename can wait).
- **Ratchet candidate:** CI grep gate with an explicit allowlist file.

### ENT-003: 🎯T10.3 “type-level egress gate” is documentation, not an API

- **Priority:** P1
- **Dimensions:** Architecture topology; Correctness / verification
- **Status:** observed fact
- **Evidence:**
  - `bullseye.yaml:125-137` — T10.3 `achieved`; acceptance requires allowlist drop, type-level gate, and an `Egress` trait (or equivalent)
  - `src/policy.rs:6-13,215-239` documents `RedactPipeline::apply_and_seal` and `RedactedFrame` with no public constructor; `new_sealed` is `#[allow(dead_code)]` “wired into egress in T10.4+”
  - `rg apply_and_seal` — comments only; no function
  - `rg Egress` — no trait
  - `src/redact/mod.rs:43-45` — only `apply(&self, frame: Frame) -> Result<Frame, …>`
  - `src/main.rs:561-567` — `pipeline.apply(frame)` then `frame.save_png`; specialists later `Read` that PNG
  - `AppAllowlist` / `frontmost_app_bundle_id` are used in `src/policy.rs` tests only; `src/main.rs` never calls them
  - `src/main.rs:590-598` — emitted `SlideEvent` sets `ocr: None`, `transcript_window: None`, `frontmost_app: None`
  - `src/redact/text_redactor.rs:4-5` — “CLI wiring lives in main.rs (not yet integrated)” plus `#![allow(dead_code)]` even though `build_redact_pipeline` *does* push `TextPiiRedactor` when `--redact-pii` is set (`src/main.rs:621-632`)
- **Mechanism:** Achieved-target text is a second source of truth. Callers can save and ship a raw `Frame` because nothing in the type system requires `RedactedFrame`. Default capture (no `--redact-*`) writes unredacted PNGs that claudia agents read — the actual SaaS egress path.
- **Blast radius:** Corporate/PII story (T10 family), any future `pageflip-feed --egress saas`, allowlist config that cannot take effect.
- **Counterevidence checked:** Face-blur and regex PII redactors exist and have unit tests. `--paranoid` saver exists. T5 was explicitly decoupled from T10 for the local Claude path. That justifies opt-in redaction; it does not justify marking the architectural gate achieved.
- **Smallest coherent remediation:** Either implement `apply_and_seal` + an `Egress` boundary on every path that leaves the machine (including specialist image reads, if that is in scope), *or* reopen T10.3 with acceptance that matches the opt-in CLI. Wire `frontmost_app` into `SlideEvent` while touching that path (🎯T26 adjacent).
- **Verification:** A compile-fail test or `trybuild` fixture that `Frame` cannot be passed to a public egress function; plus a runtime test that an empty allowlist drops frames when the gate is on.
- **Ratchet candidate:** `rg -n 'apply_and_seal|pub trait Egress'` as a file rule only after the API exists; until then, bullseye status is the bug.

### ENT-004: Slide-event protocol has four partial owners and no schema oracle

- **Priority:** P1
- **Dimensions:** Redundancy / sources of truth; Change amplification; Correctness / verification
- **Status:** observed fact
- **Evidence:**
  - `docs/slide-event-schema.json:6-8,32-84` — required `slide_id,path,t_start_ms,t_end_ms`; `additionalProperties: false`; optional `ocr`, `transcript_window`, `frontmost_app`; **no `phash` property**
  - `src/events.rs:30-47` — `SlideEvent` with `phash: Option<String>` (skip if none)
  - `meetcat/main.go:91-116` — duplicate struct with `PHash string \`json:"phash,omitempty"\`` and hand-rolled `validate()`
  - `src/bin/pageflip-feed.rs:78-82` — `SlideEventRef { path }` only
  - 🎯T18 (`bullseye.yaml:292`) — “meetcat and pageflip-feed both validate against it”
  - Producer (`src/main.rs:590-598`) never fills OCR/transcript/frontmost_app even though OCR (`src/redact/ocr.rs`) and `frontmost_app_bundle_id` exist
  - Go tests accept a rich event (`meetcat/main_test.go:27-38`) that the live producer cannot emit
- **Mechanism:** Adding a field (🎯T26 OCR bboxes, transcript window) requires coordinated edits of schema, Rust serde, Go struct, tests, STABILITY, and help text. `additionalProperties: false` means a revived `phash` would be schema-invalid if a consumer ever actually validated. Producer/consumer tests can stay green while the wire stays empty.
- **Blast radius:** meetcat decode loop, pageflip-feed `--pipe`, any external NDJSON consumer, 🎯T26/T27.
- **Counterevidence checked:** Rust `events.rs` tests cover omit-when-None and include-when-set. That tests serde, not the schema file, and not the capture loop.
- **Smallest coherent remediation:** One generated or test-shared type. At minimum, a test that (1) serialises a live `SlideEvent` from `capture_once` and (2) validates it against `docs/slide-event-schema.json`. Delete `phash` from both language structs or add it to the schema as deprecated.
- **Verification:** `go test` / `cargo test` fixture loading the JSON schema; fails if capture emits a field the schema rejects or omits a field the schema requires once T26 lands.
- **Ratchet candidate:** CI step `python -c` / `go test` schema validation over a golden NDJSON line captured from `pageflip-capture --events-out`.

### ENT-005: Region and window capture use different backends; window still hits the stale-frame API

- **Priority:** P1
- **Dimensions:** Architecture topology; Correctness / verification
- **Status:** observed fact (stale-frame *impact* on window capture: needs verification, but the split is factual)
- **Evidence:**
  - `src/capture/macos.rs:16-19,31-32,136-173` — region capture documents xcap's CGWindowListCreateImage as stale under sub-second polling; workaround is `/usr/sbin/screencapture` to a tempfile
  - `src/capture/macos.rs:61-71` — `capture_window` still calls `window.capture_image()` (xcap)
  - 🎯T24 (`bullseye.yaml:469-479`) still `identified`; context describes the same xcap staleness
  - Cargo still depends on both `screencapturekit` (audio) and `xcap` (`Cargo.toml:29,36`)
- **Mechanism:** `--region` and `--window` are presented as two ways to name a target (`src/main.rs:71-74`) but they do not share a pixel source. A meeting captured with `--window` / `--window-title` (the Teams path in T3) does not get the ScreenCaptureKit CLI workaround. Tempfile PNG per tick also means region capture is ~150 ms and disk-touching, which T24 already flags.
- **Blast radius:** Window-targeted capture (T3/T4), anyone using the default picker vs `--window`.
- **Counterevidence checked:** T24 is an open target; this finding does not reopen it as new work. It records that the workaround is path-specific, so T24's “region capture” wording can green while `--window` stays stale.
- **Smallest coherent remediation:** Drive both region and window capture through one SCK-backed implementation (crate or CLI), and add a T24 acceptance bullet for `--window`.
- **Verification:** The T24 observable (dist > 0 or `◆ [N]` after a slide advance) run once with `--region` and once with `--window-title`.
- **Ratchet candidate:** After a unified backend, a test that `capture` and `capture_window` share a function (architecture test / grep of `capture_image` = 0).

### ENT-006: 🎯T12 artifact writer is implemented and never called

- **Priority:** P1
- **Dimensions:** Correctness / verification; Documentation / governance
- **Status:** observed fact
- **Evidence:**
  - `meetcat/artifact.go:74-88` — `WriteArtifact` defined
  - `rg 'WriteArtifact\(' --glob '*.go'` — definition only; tests explicitly skip calling it (`meetcat/artifact_test.go:102-105,126`)
  - `meetcat/main.go:371-386` — shutdown writes session IDs and a resume hint, not decisions/actions/open-questions
  - `bullseye.yaml:164-178` — T12 `achieved`; context already admits “Remaining gap: wiring into the TUI stop handler”
- **Mechanism:** Status `achieved` plus a remaining-gap paragraph is a competing truth. Digest scripts (`scripts/meetings-digest.sh:12-14,67`) read `decisions.md` etc. that the live stop path never writes.
- **Blast radius:** Weekly digest (T17), resume artefact completeness, any operator expecting a meeting folder of markdown.
- **Counterevidence checked:** `WriteSessionIDs` *is* called. Manifest *is* written. The structured markdown path is the gap.
- **Smallest coherent remediation:** Call `WriteArtifact` from `runMeeting` shutdown (or reopen T12). Point `meetings-digest.sh` at files that actually exist until then.
- **Verification:** Integration test: fake neutral agent returns sectioned markdown; after `runMeeting` cancel, `decisions.md` exists.
- **Ratchet candidate:** `rg WriteArtifact` must have a production call site in `main.go` or `agents.go`.

### ENT-007: Dedup primitive is RMS; user-facing capture help still describes pHash

- **Priority:** P2
- **Dimensions:** Redundancy / sources of truth; Local code quality; Documentation / governance
- **Status:** observed fact
- **Evidence:**
  - Implementation: `src/dedup.rs:1-19,83-97` (consecutive-frame RMS); `meetcat/revisits.go:17-34,120-134` (historical RMS over downsampled PNGs) — same formula, different roles
  - `src/main.rs:67-68` clap `long_about` still says “perceptual-hash Hamming distance”
  - `src/help_agent.txt:1,73-74` — binary name `pageflip`, default interval 2, “pHash Hamming … Default: 10”
  - clap defaults are interval 1.0 and RMS 5.0 (`src/main.rs:113-122`)
  - `Cargo.toml:11` keyword `perceptual-hash`
  - `README.md:104` — “pHash-based deduplication”
- **Mechanism:** Two RMS copies can drift (Rust uses `u64` sum of squares; Go uses `float64`). Help-agent is what coding agents read (`src/help_agent.txt` changed 9 times). A threshold of 10 interpreted as Hamming bits vs RMS units is a behaviour change.
- **Blast radius:** Agent-driven flag choices; revisit false positives if Go threshold is not the same quantity as capture `--threshold`.
- **Counterevidence checked:** Dual RMS is *deliberate* — capture is last-frame, revisit is all-prior-slides. Coupling them into one crate would cross the process boundary. Duplicate *formula* should be tested against the same golden buffers, not merged into one binary.
- **Smallest coherent remediation:** Fix help_agent/clap/README/Cargo keywords. Add a shared golden RMS vector test in both languages.
- **Verification:** `pageflip-capture --help-agent` does not contain `pHash` or `Hamming`.
- **Ratchet candidate:** `rg -n 'pHash|Hamming' src/help_agent.txt src/main.rs README.md` = 0 (allow `src/dedup.rs` history comment).

### ENT-008: Specialist prompts are duplicated as Go string constants and markdown files

- **Priority:** P2
- **Dimensions:** Redundancy / sources of truth; Change amplification
- **Status:** observed fact
- **Evidence:**
  - `meetcat/agents.go:29-31,47-51` — “These strings must exactly match `meetcat/prompts/*.md`”
  - `meetcat/prompts/skeptic.md:1-3` matches `defaultSkepticPrompt`
  - `loadPrompt` falls back to the constant if the file is missing (`meetcat/agents.go:95-114`); `runtime.Caller` path fails in stripped release binaries (`meetcat/agents.go:84-92`)
- **Mechanism:** Prompt edits in markdown are no-ops in Homebrew builds; prompt edits in Go are no-ops in dev if the file exists. 🎯T13 acceptance said prompts live in config files.
- **Blast radius:** All five specialists; 🎯T30 attendee prompts will triple the copies if the pattern continues.
- **Counterevidence checked:** `//go:embed prompts/*` is *not* used. Web assets *are* embedded (`meetcat/web.go:45-46`). The embed pattern is already in the repo.
- **Smallest coherent remediation:** `//go:embed prompts/*.md`; delete the default string copies (keep a test that embed is non-empty).
- **Verification:** Changing `prompts/skeptic.md` changes `--help`-free runtime behaviour of a `go build` binary without source files beside it.
- **Ratchet candidate:** `go test` that embedded prompt bytes equal the files on disk; forbid `defaultSkepticPrompt` string bodies.

### ENT-009: Dual doctor implementations, same heading, inverted invocation docs

- **Priority:** P2
- **Dimensions:** Redundancy / sources of truth; Documentation / governance
- **Status:** observed fact
- **Evidence:**
  - Rust `src/doctor.rs:11-17,94-100` — `pageflip-capture doctor`, heading `# pageflip doctor`, table key `pageflip`
  - Go `meetcat/doctor.go:17-21,36-39` — heading `# pageflip doctor`
  - `src/doctor.rs:41-57` — capture doctor invokes `pageflip doctor` unless `--no-pageflip`
  - `docs/bug-report.md:17-18` — claims the inverse (`pageflip doctor` invokes `meetcat doctor`, `--no-meetcat`)
  - `STABILITY.md:53` — `doctor` flags `--log`, `--meetcat-log`, `--no-meetcat`
  - Rust CLI flags are `--log`, `--pageflip-log`, `--no-pageflip` (`src/main.rs:47-58`)
- **Mechanism:** Paste-ready reports will contain two identical H1s. Users following docs will pass flags that clap does not define.
- **Blast radius:** GitHub issues, T20/T21 value.
- **Counterevidence checked:** Separate concern inventories (HF models vs claude/tmux) are worth two reports. The defect is identity and docs, not the split itself.
- **Smallest coherent remediation:** Distinct H1s; align docs/STABILITY with clap; keep the capture→orchestrator passthrough as the one-command path.
- **Verification:** `pageflip-capture doctor --help` flags match `docs/bug-report.md`.
- **Ratchet candidate:** doctor `--help` snapshot test.

### ENT-010: Owner-visible web UI has no machine oracle

- **Priority:** P2
- **Dimensions:** Correctness / verification
- **Status:** observed fact
- **Evidence:**
  - `rg startWebServer|httptest|/events --glob '*_test.go'` — no matches
  - `meetcat/web.go:70` — `html.WithUnsafe()`
  - `meetcat/web.go:74-83` — specialist markdown rendered to HTML and pushed over SSE
  - `meetcat/web/{index.html,script.js,style.css}` — no Playwright/layout test
  - 🎯T23 remains `converging` pending a live session (`bullseye.yaml:455-466`)
  - journeys.md policy: owner-visible path needs a journey or an explicit exception; none in-repo
- **Mechanism:** CSS/JS/SSE regressions cannot fail CI. `html.WithUnsafe` means specialist (LLM) markdown may inject HTML into a browser that also serves local slide PNGs (`meetcat/web.go:411-428`). Binding is localhost-only (`meetcat/web.go:477`), which bounds the threat; it does not bound layout breakage.
- **Blast radius:** The primary UX (CLAUDE.md: capture → web UI → specialists).
- **Counterevidence checked:** Path traversal on `/slides/` is guarded (`pathInside`, `meetcat/web.go:419-427,511-524`). stderr sink tests exist (`meetcat/main_test.go`). Those do not render HTML.
- **Smallest coherent remediation:** One httptest that boots `startWebServer`, fetches `/`, `/events`, and a fixture PNG; plus a Playwright (or repo-standard) layout smoke. Sanitize or drop `WithUnsafe` unless a specialist actually needs raw HTML.
- **Verification:** `go test` covering GET `/` 200 and SSE `event: meeting`; a render test for a long hierarchical slide id (web-development.md).
- **Ratchet candidate:** `make go-test` includes `TestWebServerBindsLocalhost`.

### ENT-011: `meetings-digest.sh` uses bash 4 `mapfile` on a macOS-first product

- **Priority:** P2
- **Dimensions:** Local code quality; Build / release / operations
- **Status:** observed fact
- **Evidence:**
  - `scripts/meetings-digest.sh:1,46-48` — `#!/usr/bin/env bash` + `mapfile -t RECENT_FOLDERS`
  - `scripts/install-digest-cron.sh` installs a launchd job pointing at that script
  - macOS `/bin/bash` is 3.2; `mapfile` is a documented 3.2 landmine (`bash.md`)
- **Mechanism:** launchd PATH may or may not include Homebrew bash 5. If it resolves to `/bin/bash`, the weekly digest aborts. This host's interactive bash is 5.3.15, which hides the bug.
- **Blast radius:** T17 scheduled digest.
- **Counterevidence checked:** Script is otherwise modest (~146 lines) and appropriate as glue. No rewrite-to-Go indicated; only the bash 4 feature.
- **Smallest coherent remediation:** Replace `mapfile` with a `while read` loop; pin `PATH` in the launchd plist to the bash you tested, or use `/bin/bash` and stay 3.2-safe.
- **Verification:** `PATH="/usr/bin:/bin" /bin/bash scripts/meetings-digest.sh <empty-dir>` must not print `mapfile: command not found`.
- **Ratchet candidate:** CI job asserting `/bin/bash --version` is 3.x and `bash -n` plus a 3.2 run of the digest script (bash.md standing oracle).

### ENT-012: Version and Homebrew formula have three owners

- **Priority:** P2
- **Dimensions:** Redundancy / sources of truth; Build / release / operations
- **Status:** observed fact
- **Evidence:**
  - `Cargo.toml:3` — `version = "0.2.0"`
  - `meetcat/main.go:32` — `const version = "0.2.0"`
  - `packaging/pageflip.rb:10-39` — `version "0.1.0"`, old URLs, install `pageflip` + `pageflip-feed`
  - `.github/workflows/release.yml:115-128` — homebrew-releaser `install`/`test` blocks are the live formula source for new tags
- **Mechanism:** A patch to one version constant cannot update the others. `/release` may rewrite the tap from `release.yml` while `packaging/pageflip.rb` stays as a stale template someone will copy.
- **Blast radius:** `pageflip --version` vs `pageflip-capture --version` after a bump; tap install contents.
- **Counterevidence checked:** T33 already updated `release.yml`. That is the shipping path. The template and the Go const remain drift sources.
- **Smallest coherent remediation:** Generate Go version from a single file, or `-ldflags -X main.version=` in Makefile/release; make `packaging/pageflip.rb` clearly generated or delete it in favour of `release.yml`.
- **Verification:** Grep `0\.2\.0` has one authoritative hit plus generated/lock files.
- **Ratchet candidate:** release workflow fails if `meetcat` `const version` != `Cargo.toml` version.

### ENT-013: Bullseye graph does not record T33 and still tracks a pre-rename T32

- **Priority:** P2
- **Dimensions:** Documentation / governance
- **Status:** observed fact
- **Evidence:**
  - HEAD subject is `feat(T33): …` but `bullseye.yaml` has no `T33:` key
  - 🎯T32 still identified with `meetcat --version` acceptance (`bullseye.yaml:617-625`) while `release.yml` already installs `pageflip`, `pageflip-capture`, `pageflip-feed`
  - README 🎯T34 has no target
  - Graph hygiene advisory: T25 and T29 are leaves blocked by unfinished deps
- **Mechanism:** Frontier still offers “ship meetcat via brew” as ready work (bullseye startup: 🎯T32 is frontier). Doing T32 as written would fight T33.
- **Blast radius:** Next `/cv` / frontier execution.
- **Counterevidence checked:** Historical T* names inside achieved-target prose are fine. T32's *acceptance* is the live contradiction.
- **Smallest coherent remediation:** Add T33 achieved on this commit; rewrite T32 to “brew ships the T33 names” or achieve it from `release.yml`; add or drop T34.
- **Verification:** `bullseye_list` shows T33; T32 acceptance mentions `pageflip-capture` not `meetcat`.
- **Ratchet candidate:** commit-msg / CI: `feat(Tnn)` requires that id in `bullseye.yaml`.

### ENT-014: `pageflip-feed` and bubbletea-era comments are compatibility residue

- **Priority:** P3
- **Dimensions:** Redundancy / sources of truth; Local code quality
- **Status:** observed fact
- **Evidence:**
  - `src/bin/pageflip-feed.rs:22-24` still calls itself consumer of `pageflip`
  - STABILITY.md marks feed Fluid / possible pre-1.0 retirement
  - `meetcat/main.go:10,79` and `meetcat/agents.go:222-223,268` still describe a bubbletea TUI; `go.mod` still pulls charmbracelet as indirect via claudia
  - `meetcat/tui.go` is gone (6 historical commits) but comments remain
- **Mechanism:** Residue is cheap until T34 or a TUI revival. Not a defect until someone “fixes” the TUI comments by re-adding bubbletea.
- **Blast radius:** New contributors; dependency graph size (glamour/bubbletea still compile into tests via claudia).
- **Counterevidence checked:** claudia may still need charmbracelet; do not prune Go indirects without checking claudia.
- **Smallest coherent remediation:** Comment pass; T34 decision.
- **Verification:** n/a until T34 exists.
- **Ratchet candidate:** none yet.

## Redundancy and competing-source-of-truth inventory

| Fact | Owners | Drift already visible? |
|---|---|---|
| User-facing binary name | README, release.yml, Makefile vs STABILITY, bug-report.md, issue template, web title, T32 | Yes (ENT-002) |
| Capture engine name | Cargo `[[bin]]`, spawn.go vs help_agent.txt, doctor H1, clap remnants | Yes |
| Slide-event schema | JSON file, Rust struct, Go struct, feed subset | Yes (ENT-004) |
| Dedup / revisit distance | `src/dedup.rs`, `meetcat/revisits.go`, help_agent pHash | Yes (ENT-007) |
| Specialist prompts | `agents.go` constants, `meetcat/prompts/*.md` | Pattern admits it (ENT-008) |
| Doctor report | `src/doctor.rs`, `meetcat/doctor.go`, docs | Yes (ENT-009) |
| Version | Cargo.toml, `const version`, packaging.rb | Yes (ENT-012) |
| Homebrew install set | `release.yml` vs `packaging/pageflip.rb` vs T32 | Yes |
| Session log | `src/session_log.rs` vs `meetcat/log.go` | Separate event enums by design; both claim paste-safety |
| `Segment` type | `src/audio/mod.rs` vs `src/events.rs` | Independent shapes; transcript_window never filled |

Deliberate duplication to keep: process-split RMS (capture vs revisit); two doctors with different inventories; stderr sink vs web sink behind `StreamSink`.

## Healthy structure worth retaining

- **Process boundary.** Go orchestrator + Rust engine matches the constraint that macOS capture is Rust-shaped and specialist concurrency is Go-shaped (`README.md:108-112`). `spawnCapture` SIGTERM + 2s kill (`meetcat/spawn.go:39-44,82-99`) is a real lifecycle, not a leaky pipe.
- **`StreamSink`.** stderr vs web vs tests share one interface (`meetcat/agents.go:219-264`). T23 queues (`SendSlide` returns after enqueue, `meetcat/agents.go:544-554`) are the right decoupling; leave T23 converging only for the live latency observable.
- **Audio module privacy.** `AudioSamples` fields are module-private (`src/audio/mod.rs:6-23,53-61`). First-party code does not `Serialize` PCM. `scripts/transcribe.py:12-17` documents stdin-only PCM.
- **Session log schema.** Typed events without free-form content fields (`STABILITY.md:111-112`; `src/session_log.rs` tests).
- **Localhost web + slide path guard.** `127.0.0.1:49831` (`meetcat/web.go:476-477`); `pathInside` (`meetcat/web.go:511-524`).
- **RMS unit tests.** Six `src/dedup.rs` tests cover first frame, identical, sub-threshold, super-threshold, dimension change, zero threshold. Go `revisits_test.go` exists (not re-run this pass).
- **Redact pipeline composability.** `Redactor` trait + `RedactPipeline::push` (`src/redact/mod.rs:17-39`) is the right seam *once* it is actually the egress boundary.
- **Capture trait.** `src/capture/mod.rs:97-99` still allows a non-macOS backend; T6/T7 were deleted and Linux release jobs stripped — the trait is unused extra, not a cycle.
- **Paste-safe doctor intent.** Even with heading bugs, T20/T21 remain the right bug-report shape.
- **Apache-2.0 + SPDX** on source files; `LICENSE` present.
- **Rust test layer.** 106 + 5 tests green on this snapshot in debug.

## Hygiene posture

`hygiene.yaml` is **absent**. Per the hygiene skill: **hygiene posture not declared.** The validator was invoked anyway and did not produce a posture vector:

```
FileNotFoundError: [Errno 2] No such file or directory:
'/Users/marcelo/work/github.com/marcelocantos/pageflip/hygiene.yaml'
exit 1
```

No floors, held tiers, or planned/skipped items exist to drift against. This audit did **not** initialise hygiene.

Informal overlap (not hygiene findings): LICENSE + README + `.gitignore` exist; `make bullseye` is declared but Go-red on this snapshot; CI is macos-14 only; no secret scan, SBOM, or `cargo deny` job; issue templates exist.

Entropy findings that could become hygiene items after adoption: ENT-001 (`GOWORK=off` + no path replace), ENT-002 (meetcat-name allowlist), ENT-004 (schema validation command), ENT-007 (no pHash in help_agent), ENT-011 (bash 3.2 job).

## Oracle coverage and residue

| Property | Decision path | Notes |
|---|---|---|
| RMS consecutive dedup | shipped unit tests (`src/dedup.rs`) | Green this pass (debug). |
| SlideEvent serde omit/include | shipped unit tests (`src/events.rs`) | Does not load JSON schema; does not run `capture_once`. |
| Go event validate() | shipped `meetcat/main_test.go` | **Not executed** this pass (ENT-001). |
| Capture freshness | none | 🎯T24; live meeting only. |
| Window vs region pixels | none | ENT-005. |
| Redaction-before-egress | comments + dead type | ENT-003. |
| PII regex / face-blur math | shipped unit tests | Synthetic buffers, not Vision on a real frame. |
| Audio never on disk | type privacy + transcribe.py comments | No test that fails if a `.wav` appears; WhisperX internals unverified. |
| Specialist prompt content | none for embed | ENT-008. |
| Web UI / SSE / layout | none | ENT-010. |
| `make bullseye` | CI on master | Red on this branch's Go recipes. |
| Schema file vs producer | none | ENT-004. |
| Owner journey (capture → UI → specialist) | manual (CLAUDE.md real sessions) | No journey harness. |

**Owner residue (intent / taste, not mechanical follow-up):**

1. Is specialist image upload to Anthropic in-scope for the T10 egress gate, or is “local Claude Code” an accepted exception forever?
2. Keep the `meetcat/` directory name after the binary rename, or rename the module in a follow-up?
3. Retire `pageflip-feed` before 1.0 (README 🎯T34) or keep it as a scripted pipe?
4. Accept `html.WithUnsafe` for specialist markdown on localhost?

**Mechanical residue this audit did not run:** `cargo clippy --release`, `cargo test --release`, `GOWORK=off` tests after publishing claudia, live T24 session, WhisperX subprocess filesystem trace.

## Remediation sequence

1. **Unblock the gate (ENT-001).** Publish or vendor claudia so `replace` can die; `GOWORK=off` in Makefile; confirm `make go-test` on a clean tree. Until this is green, do not treat `make bullseye` as an oracle.
2. **Converge identity (ENT-002, ENT-009, ENT-012, ENT-013).** Record T33; rewrite T32; one doctor heading pair; STABILITY + bug-report + issue template; version ldflags. This is one coherent pass, not a docs-only follow-up.
3. **Tell the truth about T10.3 and T12 (ENT-003, ENT-006).** Reopen or implement. Do not add more redactors until the gate and the stop-path artefacts match their targets.
4. **Single-source the event protocol (ENT-004), then OCR (T26).** Schema test before filling `ocr`/`frontmost_app`.
5. **Unify capture backends (ENT-005 / T24)** including `--window`.
6. **Embed prompts (ENT-008), fix help_agent (ENT-007), bash 3.2 digest (ENT-011), web httptest (ENT-010).**
7. **Ratchet** the accepted properties in CI. Hygiene.yaml only if the owner asks to onboard.
8. Re-run this audit on the same finding IDs.

No rewrite of the Go/Rust split is required. The architecture is the rename that did not finish, plus gates that were marked achieved at the module level.
