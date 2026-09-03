# Logging Levels and Failure Capture — Design

**Date:** 2026-09-02
**Status:** Approved for planning

## Problem

Three complaints, which turn out to have three different causes.

### 1. The log is mostly one line, repeated

`realRunner.Run` logs at INFO on both entry and exit
(`internal/makemkv/executor.go:44` and `:54`). It is shared plumbing: the same
function serves a real disc scan and the background drive poll, and it cannot
tell them apart. `ListDrives` (`executor.go:173`) runs
`makemkvcon -r --cache=1 info disc:9999` on every poll cycle — default interval
5 seconds (`internal/config/config.go:37`), and each invocation takes roughly
5 seconds, so the observed cadence is one pair every ~10 seconds:

```
{"msg":"makemkvcon: executing","args":["-r","--cache=1","info","disc:9999"]}
{"msg":"makemkvcon: command completed","args":[...],"output_bytes":698}
```

That is about **17,000 lines a day** stating that nothing happened.

`drivemanager` is not implicated. Its poll logging is already gated behind
`isFirst` (`internal/drivemanager/manager.go:125` and `:165`), so it announces
the drive layout once and then goes quiet. The noise has a single source.

### 2. There is no level control, anywhere

`main.go:31` constructs the handler with `nil` options:

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
```

`nil` means the default, and the default floor is INFO. There is no
`BLUFORGE_LOG_LEVEL`, no YAML key, and no UI control. The consequence is that
the codebase's 7 existing `slog.Debug` calls — including two deliberate ones in
the drive poller — can never print. They are dead code.

The distribution reflects this: **116 `slog.Info` against 7 `slog.Debug`.**
With no level to select, every author had one honest choice, and everything
became INFO.

### 3. Failures are thin, and assert causes they have not proven

A failed rip persists one line, `rip_jobs.error_message`, and the messages that
would explain it are parsed and discarded. Worse, the strings claim more than
they know. The failure that prompted this work read:

```
makemkv: title 00200.mpls is not in this pass (index 3 is absent);
the drive did not read it this time
```

The drive had read it perfectly. The title was announced under message code
3308 rather than 3307, because it is a multi-angle title, and the guard was not
watching 3308. The clause `the drive did not read it this time` was a guess
presented as a finding, and it sent the investigation at the hardware for the
first twenty minutes. The observation the guard actually had was narrower:
`00200.mpls was not in MakeMKV's list for this pass`.

## Goals

1. A default log where every line corresponds to something happening.
2. A level control, so the firehose is available without a code change.
3. Detail for a failed rip captured automatically, and reported with the
   failure rather than gated behind a level that was not on when it happened —
   closing the gap scans already have covered.
4. Error strings that state what was observed and stop asserting causes.

## Non-goals

- **Persisting captured detail.** Decided against; see "Decisions taken".
- **A settings-page toggle for the level.** The mechanism leaves room for one;
  building it is not in scope.
- **Sweeping all 116 INFO calls.** Scope is the makemkvcon operations — rip,
  scan, backup — and the runner beneath them. The rest are not causing harm and
  a blanket re-level would be a large, low-value diff.
- **Changing the log format.** JSON to stdout stays.

## Decisions taken

**Level is read from the environment only, not YAML.** `config.Load` logs while
it runs, so a level sourced from `/config/config.yaml` could not govern the
lines emitted before the file was read. An env var is available from process
start. Precedence elsewhere in BluForge is defaults → env → YAML; this setting
deliberately sits outside it.

**INFO is decided by operation, not by verbosity.** The tempting rule — "MakeMKV
chatter is verbose, so demote it" — is wrong here. The lines that diagnosed the
angle bug were exactly that chatter, and they are only emitted while a rip is
running. The distinction that matters is whether a line belongs to a real disc
operation or to a background poll that found nothing. Verbosity during a
30-minute rip is wanted; a heartbeat at idle is not.

**Captured detail lives in memory only, and does not survive a restart.** Chosen
by John over persisting to SQLite. The cost is real and is accepted: the
container restarts to deploy the fix for the very bug being diagnosed, and the
evidence goes with it. Recorded here so the trade-off is visible if it later
proves annoying. The precedent for the other choice exists — `disc_diagnostics`
(migration 008) persists per-scan evidence to SQLite with a `detail` column,
written so "a recurrence is diagnosable without repeating the whole
investigation" — and reversing this decision is a migration plus a write, not a
redesign.

**The capture rides on the error, not on the level.** John's requirement: "that
kind of error should log regardless of level." A failure emits its captured
messages as part of the ERROR record. Nothing about reporting a failure is
conditional on DEBUG having been enabled beforehand.

## Design

### 1. Level control

`main.go` reads `BLUFORGE_LOG_LEVEL` once, before anything else logs, and feeds
a `slog.LevelVar` into the handler options:

| Value | Level |
|---|---|
| `debug` | `slog.LevelDebug` |
| `info` (default) | `slog.LevelInfo` |
| `warn` | `slog.LevelWarn` |
| `error` | `slog.LevelError` |

Parsing is case-insensitive. An unrecognised value falls back to `info` and logs
a WARN naming what it saw — a typo in a compose file must not silence the
application.

`LevelVar` rather than a plain `slog.Level`: it is the same amount of code and
it makes the level swappable at runtime, so a settings toggle later is a wiring
change rather than a rework. No toggle is built now.

In the README's settings table the Setting column takes `*(optional)*`, the
convention already used for `MAKEMKV_KEY` and the other env-only entries. The
paragraph above that table says every setting is also editable from the Settings
page, which will not be true of this one; it needs a qualifier.

This alone brings the 7 existing `slog.Debug` calls to life.

### 2. Re-levelling

The rule: **the runner is plumbing and logs at DEBUG. Each operation logs
itself, at the level that operation deserves.**

| Site | Now | After |
|---|---|---|
| `realRunner.Run` entry/exit (`executor.go:44`, `:54`) | INFO | DEBUG |
| `realRunner.Run` failure | ERROR | ERROR (unchanged) |
| `ListDrives` — the poll | (via runner) INFO | nothing at INFO |
| `ScanDisc` / rip / backup | (via runner) INFO | INFO, logged by the operation |
| `makemkvcon message` during rip/scan (`logging.go:28`) | INFO | INFO (unchanged) |

The operations gain their own start and finish lines, so a scan remains visible
at INFO without the poll riding along on the same statement. `logMakeMKVEvent`
is untouched: it only fires during a real operation, which is precisely the
case the rule keeps.

Net effect at INFO: the ~17,000 daily poll lines go, and everything that
happens because a disc was inserted stays.

### 3. Failure capture

`streamRip` (`executor.go:952`) already parses every event on the rip's output
stream. It accumulates the `MSG` events — code and text — into a bounded slice.
`PRGV` is excluded: progress is the volume, and it carries nothing diagnostic
that the deciles already logged do not.

Sizing: the failing Kiki's Delivery Service rip produced about 25 messages for
the whole enumeration. A cap of a few hundred is a safety valve against a
pathological disc, not an expected limit. When the cap is hit the oldest are
dropped and the slice records that it was truncated, so a reader is never shown
a partial list that looks complete.

`ripOutcome` (`executor.go:1012`) emits the captured slice as a structured field
on the ERROR it already returns, on all three failure paths it distinguishes —
guard objection, `copyFailed`, non-zero exit.

The same slice attaches to the in-memory `ripper.Job` alongside `Job.Error`
(`internal/ripper/job.go:65`) and surfaces on the activity page behind a
disclosure — the pattern `ScanOutput` already uses on the drive page
(`internal/web/json_helpers.go:238`). It is not written to `rip_jobs`, so a job
reloaded from the database after a restart shows its error and no capture. The
UI must render that absence as ordinary rather than as an error state; most
failed jobs a user looks at will be in this condition.

Scans do not need this. A scan's messages are already retained and already
surfaced — `ScanOutput` keeps them unfiltered and `Diagnose` interprets them,
both reachable from the drive page. The gap is the rip, whose messages reach
`logMakeMKVEvent` (`executor.go:971`) and nothing else. Backups are the same
gap (`executor.go:814`) and get the same capture; whether the backup UI grows a
disclosure for it is left to the plan, since a failed backup already reports
through the recovery path.

### 4. Error text

Two rules, both derived from patterns already in the codebase rather than
invented for this change.

**State the observation, not the cause.** An error reports what was seen. Where
a cause is genuinely known it may be named; where it is inferred it is left out.
The guard's message becomes a statement of what the enumeration contained, with
no claim about why.

**Diagnosis keeps its own channel.** BluForge already separates what happened
(`Message`, `ScanOutput`) from what it means (`ScanDiagnosis`, `ScanFinding`).
Error strings describe; findings interpret. The `the drive did not read it`
clause was an interpretation smuggled into a description, and the split already
existed to prevent exactly that.

User-facing strings follow the standing rule: plain and literal, in the words a
person would use, each one standing alone without leaning on a neighbouring
string for sense.

## Testing

Assertions are on emitted records via a capturing `slog.Handler`, not on
formatted text — the same reasoning that makes the MakeMKV parser read
parameters rather than prose.

| Behaviour | Test |
|---|---|
| Level parsing | each accepted value; case-insensitivity; an unrecognised value falls back to `info` and warns |
| The poll is quiet | a poll cycle at INFO emits no records; at DEBUG it emits them |
| A scan is not | a scan at INFO still emits its own start/finish |
| Capture on failure | each of the three `ripOutcome` paths carries the messages |
| Capture is not level-gated | the failure record carries its messages with the level at `info` |
| The bound holds | a flood truncates, is marked truncated, and does not grow without limit |
| Error text | the guard's message states the observation and names no cause |

## Files touched

| File | Change |
|---|---|
| `main.go` | read `BLUFORGE_LOG_LEVEL`, build the handler with a `LevelVar` |
| `internal/makemkv/executor.go` | runner to DEBUG; operations log themselves; capture in `streamRip`; report in `ripOutcome` |
| `internal/makemkv/titleguard.go` | error text states the observation |
| `internal/ripper/job.go` | in-memory field for the captured messages |
| `internal/web/json_helpers.go` | expose the capture to the activity page |
| `templates/activity.templ` | disclosure, following the `ScanOutput` pattern |
| `README.md` | add `BLUFORGE_LOG_LEVEL` to the settings table |

## Risks

**The re-levelling hides something that mattered.** Mitigated by the rule's
shape: only the poll loses its INFO lines, and it is the one operation that runs
whether or not anything is happening. Anything reachable only during a real disc
operation keeps its level.

**The capture is absent exactly when wanted.** A consequence of memory-only
storage, accepted above. The ERROR record still reaches `docker logs`, so the
detail is lost from the UI rather than lost entirely, as long as the container's
logs are retained.

**Rewritten error strings break a habit.** Several of these strings have been
read for months and will now read differently. This is intended; the old
phrasing cost a debugging session.
