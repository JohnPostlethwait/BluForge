# Spurious-AACS Disc Recovery — Design

**Date:** 2026-08-09
**Status:** Approved for planning

## Problem

Some retail Blu-ray/UHD discs ship with a complete AACS directory (MKB,
certificates, protection scaffolding) but with the MPEG-TS payload left
unencrypted. This is a mastering/replication defect, not a protection scheme.
It is scattered and unpredictable — confirmed on 3 of 25 discs in one box set,
spanning non-contiguous seasons — so it cannot be predicted from title, season,
or press batch.

MakeMKV decides whether to run the AACS path purely on the presence of the AACS
directory. When present it demands a volume key. For these discs no volume key
exists, because nothing is encrypted. MakeMKV fails with a misleading error that
sends users hunting for keys and key databases.

The confirmed workaround: back up the disc *without* decryption, delete the AACS
directory from the backup, then rip from the resulting folder. MakeMKV then logs
"AACS directory not present, assuming unencrypted disc" and completes normally.
Verified on three discs.

Critically, this failure is **indistinguishable from a genuine missing-volume-key
case based on MakeMKV output alone**. The workaround must never be applied blind.

## Goals

1. Detect the failure signature and determine which case it is by inspecting the
   payload directly.
2. On a confirmed false flag, recover automatically — back up, strip AACS,
   re-scan, and rip — while respecting the same track selection a cleanly
   scanning disc would have received.
3. On a genuine missing-key case, report it as such and surface the `.tgz` dump
   path for submission to `svq@makemkv.com`.
4. Persist enough per-disc evidence that a recurrence is diagnosable without
   repeating the investigation.
5. Clean up the scratch backup afterwards.

## Non-goals

- Any attempt to obtain, derive, or work around a genuine volume key. Genuinely
  encrypted discs are reported and left alone.
- DVDs. They use IFO/VOB and CSS, not AACS; the detection path no-ops on them.
- A user-facing toggle to disable recovery. Recovery is automatic by decision;
  see "Decisions taken".

## Corrections to the original writeup

Two errors and one consequence, all found while designing the detector, all of
which change the implementation.

### 1. Blu-ray `.m2ts` uses 192-byte packets, not 188

The writeup specifies 188-byte packets with sync byte `0x47` at each packet
start. That describes plain `.ts`. Blu-ray `.m2ts` (BDAV) prefixes every 188-byte
TS packet with a 4-byte `TP_extra_header` timecode, giving a **192-byte stride**
with `0x47` at offset 4 within each unit.

A detector locking to a 188-byte stride will fail to find sync on a real
`.m2ts` and return "unknown" for every disc — including the healthy ones.

The implementation locks stride by trial: it tries 192 first, then 188, and uses
whichever produces consecutive sync bytes. This keeps it correct for both
container layouts.

### 2. `0x03` mask with `>> 6` is contradictory

The writeup gives `transport_scrambling_control` as "bits 6-7 of byte 3 (`0x03`
mask, `>> 6`)". Masking with `0x03` and then shifting right by 6 always yields
zero — i.e. "never scrambled". The intent is the top two bits of byte 3:

```go
tsc := (pkt[3] >> 6) & 0x03   // 00 = clear, 01/10/11 = scrambled
```

Equivalently `pkt[3] & 0xC0`. The implementation uses the shift-then-mask form.

### 3. Consequence: sync lock is itself a signal

AACS on Blu-ray encrypts in 6144-byte Aligned Units (32 source packets of 192
bytes). The first 16 bytes of each unit are plaintext; bytes 16..6143 are
ciphertext. So on a genuinely encrypted disc, the sync bytes of packets 1..31
within each unit are ciphertext and **will not lock at a 192-byte stride** — the
TSC bits of those packets are not readable in the first place.

This means a naive "read TSC bits" detector would see garbage rather than a
clean scrambled/not-scrambled answer. The detector therefore uses two signals:

| Observation | Verdict |
|---|---|
| Sync locks at 192 across consecutive packets, no TSC bits set | `unencrypted` |
| Sync locks, some TSC bits set | `scrambled` |
| Sync fails at 192, but `0x47` present at each 6144-byte boundary + 4 | `scrambled` (AACS aligned units) |
| Neither | `unknown` |

Only `unencrypted` triggers recovery. Both `scrambled` and `unknown` are
reported and stop the pipeline — the safe direction for an ambiguous answer.

A fourth value, `n/a`, is recorded for discs where inspection never ran: any
disc that scanned cleanly (the `direct` path), and DVDs.

## Detection signature

In `makemkvcon -r` robot mode, a spurious-AACS disc produces:

- `MSG:3303` — "The volume key is unknown for this disc - video can't be decrypted"
- `MSG:5010` — "Failed to open disc"
- `TCOUNT:0`

Matched on numeric codes only; message text is localized and unstable.

The check requires all three, matching the writeup exactly. To keep a variant
signature diagnosable, **every** zero-title scan logs the full set of message
codes it saw, whether or not the signature matched.

`MSG:5042` ("no usable optical drives") is explicitly **not** part of this
signature. It appears on nearly every `makemkvcon` invocation including
successful ones, and is only meaningful when the source is `disc:N`. It gets its
own handling — see "Drive enumeration under Docker".

## Architecture

### The core idea: the backup is an alternate *source*, not an alternate pipeline

Today `Executor.ScanDisc` and `Executor.StartRip` hardcode `disc:N`. Generalizing
that to a two-form `Source` lets recovery slot into the existing pipeline instead
of running beside it:

```
scan disc:N  →  fails 3303/5010/TCOUNT:0
             →  mount disc, sample .m2ts packets  →  unencrypted
             →  free-space check
             →  makemkvcon backup (no --decrypt)  →  remove <backup>/AACS
             →  re-verify packets on the backup
             →  scan file:<backup>  →  title list
             →  normal pipeline: match, select tracks, rip, organize
             →  delete backup when the last job for the disc finishes
```

From the pipeline's perspective a recovered disc is just a disc that scanned
successfully. DiscDB matching, per-title destinations, track selection,
duplicate handling, progress, and contributions all work unmodified. This is
what makes "respect the track selection" fall out for free rather than needing
to be re-implemented.

The rejected alternative was a self-contained `RecoverAndRip` calling
`makemkvcon mkv file:<dir> all <output>` as the writeup literally specifies.
`all` rips every title with every track: no track selection, no per-title
destinations, no DiscDB match, no per-title progress.

### Components

| Package | Change |
|---|---|
| `internal/makemkv` | New `Source` type; `ScanSource`/`StartRip` take a `Source`; new `Backup` operation; typed `ScanError` carrying the partial scan; signature and 5042 predicates |
| `internal/aacs` | **New.** Pure detection: AACS dir presence, `.m2ts` packet inspection, verdict |
| `internal/mpls` | Extract `OpenDiscRoot` (mount/locate + cleanup) and `ReadFrom(root)` from the existing mount machinery so both a device and a plain directory can be read |
| `internal/ripper` | `Job` carries a `Source`; `RipExecutor` interface takes it |
| `internal/workflow` | `recovery.go` — orchestration, scratch lifecycle, refcounted cleanup |
| `internal/db` | `disc_diagnostics` table + store methods |
| `internal/drivemanager` | `StateRecovering`; optical-access self-check |
| `internal/web` | SSE `disc_recovery` events; recovery banner on drive detail |

### `makemkv.Source`

```go
type SourceKind int
const (SourceDisc SourceKind = iota; SourceFile)

type Source struct {
    Kind       SourceKind
    DriveIndex int    // SourceDisc
    Path       string // SourceFile
}

func DiscSource(i int) Source
func FileSource(p string) Source
func (s Source) Arg() string   // "disc:0" | "file:/path/to/backup"
func (s Source) IsDisc() bool
```

`ScanDisc(ctx, driveIndex)` is retained as a thin wrapper over
`ScanSource(ctx, DiscSource(i))` so the `DiscScanner` and `DriveExecutor`
interfaces used by `workflow` and `drivemanager` keep their shape.

### MPLS enrichment for folder sources

`enrichScanFromMPLS` currently takes a device path and mounts it. It becomes
root-based: for a disc source the root is the mount point, for a file source it
is the backup directory. Recovered discs therefore get MPLS language enrichment
straight from the backup with no mount at all — a small side benefit, since the
backup contains the full `BDMV/PLAYLIST/` tree.

`mpls.ReadDiscLanguages` keeps its signature and is reimplemented on top of the
extracted `OpenDiscRoot` + `ReadFrom`.

### Scratch layout and lifecycle

Scratch lives under the configured output directory, per decision:

```
<output_dir>/.bluforge-scratch/<sanitized-disc-label>-<short-hash>/
```

The leading dot keeps it out of Plex/Jellyfin scans and matches the existing
`.rip-*` temp convention in `ManualRip`. No new config setting — it inherits
whatever volume mapping `output_dir` already has, so existing installs work
without a compose change.

Lifecycle:

- **Created** immediately before the backup starts, after the free-space check.
- **Deleted** by the same `wg.Wait()` goroutine in `ManualRip` that already
  cleans up the parent temp dir, once every job for the disc has finished.
- **Deleted** on disc eject or scan invalidation when no jobs are outstanding
  (refcount zero).
- **Retained** on any failure, with the path reported in the error, the log, and
  the diagnostics row.
- **Swept** at startup: any `.bluforge-scratch/*` left behind by a crash is
  removed before the first poll.

### Guarding the AACS deletion

Deleting a directory is the one destructive step. It is constrained to make a
mistake structurally impossible:

- The path removed is exactly `<scratchRoot>/<slug>/AACS`.
- Before removal, the resolved absolute path must be verified to sit inside the
  resolved scratch root — the same containment check `processTitle` already uses
  for destination paths.
- Removal only runs after the backup command exits successfully.
- The disc itself is never modified; only the on-disk copy.

## Data flow

`Orchestrator.ScanDisc` gains the recovery branch:

1. `ScanSource(DiscSource(n))`.
2. On success — record a `direct` diagnostics row, return as today.
3. On failure, type-assert `*makemkv.ScanError` to reach the parsed messages.
   Today a failed scan returns a bare `error` and the messages are lost; the new
   typed error carries the partial `*DiscScan` so the signature can be checked.
4. Signature match → recovery. No match → return the original error unchanged.
5. Recovery returns `(Source, cleanup, error)`. On success, re-scan from the
   file source, cache it under the same drive index, and record the recovered
   source in `Orchestrator.recovered[driveIndex]` so rip jobs target it.
6. `ripper.Job.Source` is populated from that map; the engine passes it to
   `StartRip`. Queue keying stays on `DriveIndex` — `Executor.mu` serializes all
   `makemkvcon` calls anyway, so nothing is gained by treating a folder rip as
   independent of the drive.

## Progress and user notification

Recovery is automatic, so the user learns about it rather than authorizing it.

- New drive state `StateRecovering`, so the drive is not shown as idle for the
  ~40 minutes a UHD backup takes.
- `makemkvcon backup` emits `PRGV` in robot mode; those are wired to an
  `onEvent` callback exactly as `StartRip` does.
- New SSE event `disc_recovery` carrying
  `{drive_index, disc_name, phase, percent, message}` where phase is
  `detecting | backing_up | stripping | rescanning | done | failed`.
- The drive detail page shows a banner for the active phase; a completed
  recovery leaves a persistent "recovered via AACS workaround" badge sourced
  from the diagnostics row.

**Known pre-existing limitation:** a backup holds `Executor.mu` for its full
duration, so drive polling stalls meanwhile. `StartRip` already behaves this way
for the length of a rip, so this is not a regression, and progress still reaches
the UI over SSE. Out of scope here.

## Observability

New table, migration `008_add_disc_diagnostics.sql`:

```sql
CREATE TABLE IF NOT EXISTS disc_diagnostics (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    disc_label        TEXT NOT NULL,
    disc_key          TEXT NOT NULL DEFAULT '',
    drive_index       INTEGER NOT NULL DEFAULT 0,
    mkb_version       TEXT NOT NULL DEFAULT '',
    aacs_dir_present  INTEGER NOT NULL DEFAULT 0,
    scramble_verdict  TEXT NOT NULL DEFAULT '',  -- unencrypted | scrambled | unknown | n/a
    packets_checked   INTEGER NOT NULL DEFAULT 0,
    scrambled_packets INTEGER NOT NULL DEFAULT 0,
    rip_path          TEXT NOT NULL DEFAULT '',  -- direct | backup_strip | blocked
    outcome           TEXT NOT NULL DEFAULT '',  -- ok | failed
    detail            TEXT NOT NULL DEFAULT '',
    dump_path         TEXT NOT NULL DEFAULT '',
    backup_bytes      INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_disc_diagnostics_label ON disc_diagnostics(disc_label);
CREATE INDEX IF NOT EXISTS idx_disc_diagnostics_created ON disc_diagnostics(created_at);
```

One row per disc scan, not just per anomaly — knowing that the other 22 discs in
a box set took the `direct` path is part of what makes the 3 diagnosable.
`disc_key` is filled in only after a successful scan, because
`discdb.BuildDiscKey` hashes the title list and degrades to a useless constant
when the scan returned zero titles.

`aacs_dir_present` is nearly free on the success path: MPLS enrichment already
mounts the disc on every scan, so it is one extra `stat`. It is best-effort —
when the mount is unavailable the field stays `0` and `detail` records that the
check did not run, so a `0` is never mistaken for a confirmed absence.

**MKB version and dump path** are best-effort and never fail the pipeline:
scan messages are searched for a `MKB<n>_v<n>_*.tgz` path, falling back to a
glob of `$HOME` and the working directory for recently modified matches. The
version is parsed from the filename (e.g. `MKB20_v82` → `20 v82`).

## Error handling

| Condition | Behavior |
|---|---|
| Signature matched, verdict `scrambled` | No backup. Diagnostics `rip_path=blocked`. Error names it a genuine missing-volume-key case and includes the `.tgz` dump path for `svq@makemkv.com`. |
| Signature matched, verdict `unknown` | No backup. Same as above but wording says the determination was inconclusive and gives the reason (no `BDMV/STREAM`, no sync lock, unreadable). |
| Disc cannot be mounted | No backup — reported as "could not determine", never guessed. Mounting is already load-bearing for MPLS enrichment on every scan, so this is an established assumption rather than a new one. |
| Insufficient scratch space | Fails before the backup starts. Required size is computed by walking the mounted disc and summing file sizes, plus 5% headroom — more accurate than a hardcoded 100GB. Message states required vs available and the scratch path. |
| Backup fails | Backup retained for inspection; path reported. If `MSG:5042` is present, the drive-access error below is returned instead of a bare "backup failed". |
| Re-verification of the backup disagrees with the disc | Abort, retain backup, record `outcome=failed` with the disagreement. Should not happen; treated as a bug signal rather than something to paper over. |
| Folder re-scan yields zero titles | Abort, retain backup, report that the stripped backup still did not scan. |

## Drive enumeration under Docker

Separate issue, same pass. `makemkvcon` enumerates drives via `/dev/sg*`, which
are `root:disk` (GID 6) mode `0660`. A container process running as `99:100`
sees no drive and fails with `MSG:5042` plus a bare "Backup failed".

`entrypoint.sh` already detects the GID owning `/dev/sr*` and `/dev/sg*` and adds
it as a supplementary group (lines 123-144), so the container side is handled.
The gap is Go-side reporting:

- `ListDrives` returning zero drives **with** `MSG:5042` returns a typed
  `ErrNoOpticalDrives` whose message names the cause and the fix (process must
  be in the group owning `/dev/sg*`, commonly `disk`/GID 6; `group_add: [6]` in
  compose).
- A startup self-check logs a distinct warning when running non-root on Linux
  and `/dev/sg*` exists but is not readable by the current process.
- `MSG:5042` remains ignored for file sources, where it is pure noise.

## Directory creation mode

Already fixed by commit `4aa1706`, which changed the output path's `MkdirAll` to
`0o777` in `internal/organizer/organizer.go:40`. The only remaining explicit
modes are the MPLS mount point and the `/config` persist dir, neither of which is
in the output path. This pass verifies rather than re-fixes; scratch directories
are created with the same `0o777` for consistency.

## Testing

Detection is pure and gets the heaviest coverage, because it is the part that
decides whether to spend 100GB.

- **Packet inspection** — table-driven over synthetic `.m2ts` built in-test:
  clean 192-byte stride; 188-byte stride; TSC bits set; AACS-style aligned units
  (16 plaintext bytes then random); truncated file; file with no sync anywhere;
  no `BDMV/STREAM` directory. Each asserts an exact verdict.
- **Signature matching** — table-driven over `[]makemkv.Message`: all three codes
  present; each one missing; codes present but titles non-zero; `5042` present
  alongside (must not affect the outcome).
- **`Source`** — `Arg()` formatting for both kinds.
- **Recovery orchestration** — mock runner plus a fake disc root in `t.TempDir()`
  containing `BDMV/STREAM/*.m2ts` and an `AACS/` dir. Asserts: AACS removed,
  file source returned, cleanup deletes the backup, backup retained on failure.
- **Containment guard** — an `AACS` path resolving outside the scratch root is
  refused.
- **Free space** — insufficient space fails before any backup command is issued
  (asserted against the mock runner's call log).
- **Track selection preservation** — end-to-end from failed disc scan through
  recovery to job submission, asserting the generated selection string reaches
  the runner. This is the requirement most at risk of silent regression.

Existing `RipExecutor` mocks need updating for the `Source` parameter.

## Documentation

New `TROUBLESHOOTING.md` covering:

- The symptom and the misleading key-hunt it triggers.
- The `MSG:3303` / `MSG:5010` / `TCOUNT:0` signature, by code not text.
- How to distinguish spurious-AACS from a genuine missing key, including the
  192-byte stride and aligned-unit facts above.
- The manual workaround steps, for users not running BluForge.
- `MSG:5042` and the `/dev/sg*` group requirement.
- **A clean `ddrescue` image (100% rescued, zero bad sectors) rules out physical
  media problems entirely.** That check cost an hour during the investigation;
  documenting it means nobody repeats it.

README gains a link.

## Decisions taken

- **Automatic recovery, no toggle.** Requested explicitly. A confirmed false-flag
  disc recovers end-to-end without interaction; the user is informed via SSE and
  the drive state, not asked.
- **Scratch under `output_dir`**, hidden, no new setting.
- **Track selection is whatever the disc would have gotten had it scanned
  cleanly** — config-derived options under auto-rip, on-screen picks in the
  manual flow. These discs fail with `TCOUNT:0`, so no selection for the disc can
  exist before recovery; the point is that recovery must not degrade to `all`.
- **Ambiguity stops the pipeline.** `unknown` is treated like `scrambled`.
