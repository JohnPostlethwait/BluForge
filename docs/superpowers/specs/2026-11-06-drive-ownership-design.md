# One owner per drive

Approved by the user 2026-11-06. Built on branch `worktree-dvd-salvage`.

## Why

Every drive hang in this project's history was BluForge touching a drive while
something else was driving it, or touching it in a state it could not take:

| When | What BluForge did | Commit |
|---|---|---|
| 2026-09-02 | Left a UDF mount live after the media changed; the kernel retried until the USB bridge stopped answering. | 8e1cb8b |
| 2026-09-07 | Kept makemkvcon running against a pulled disc. | a079c1a |
| 2026-09-08 | Forgot a mount whose umount failed. | a1893f4 |
| 2026-11-04 | Two makemkvcon processes reached one drive (per-drive locks without isolation). BU40N reset storm. | 697e07b → 6d0449e |
| 2026-11-06 | blkid read a just-arrived disc, outside any lock, while a drive listing (`info disc:9999`, which probes every drive) drove it. Listing timed out; the BU40N's AACS key exchange then failed. | e24f344 |

Each was fixed one at a time. The rule they all break: code reached into a
drive outside the lane of whatever owned it. The user's requirement: once
something has the ball (a drive with a disc) it holds it, nobody else touches
that drive until it lets go, and this is enforced by structure, not by care.

## Design

### 1. `internal/drives` owns every drive touch

The only package that opens `/dev/srN` or `/dev/sgN`, or runs a process against
one: makemkvcon on a drive, blkid, mount/umount, ddrescue, the status ioctl.

```go
broker.Do(ctx, driveIndex, "rip Earthquake t3", func(ctx context.Context, d *drives.Drive) error {
    scan, err := d.Scan(ctx, onEvent)
    root, release, err := d.OpenRoot(ctx)   // UDF mount, released before Do returns
    err = d.Rip(ctx, ...)
    err = d.Rescue(ctx, opts, onProgress)   // ddrescue
    ...
})
```

- `*Drive` is valid only inside the callback; after it returns every method
  fails. It cannot be leaked to run later.
- No device path leaves the package except for display (`DevicePath()` on the
  drive listing, read-only, for the UI and logs).
- Mounts opened through `OpenRoot` are force-released when `Do` returns.

### 2. Exclusive, held for the whole operation

- `Do` on a drive waits for that drive's current holder (cancellable through
  ctx). Work on other drives is unaffected when isolation works.
- A callback already holding drive N that calls `Do` for drive N gets
  `ErrAlreadyHeld` at once (detected through ctx), never a deadlock. This is the
  class `salvage_lock_test.go` exists for.
- Work that reads only a copy on disk (`file:`/`iso:`) goes through
  `DoDetached`: with isolation it runs makemkvcon with no drive visible and
  holds nothing; without isolation it would open every drive, so it holds every
  drive.

### 3. No global touch

The 5-second `makemkvcon info disc:9999` poll is removed.

- Which drives exist: sysfs (`/sys/class/block/sr*`), a file read with no
  drive I/O. Drive index = N of `/dev/srN`.
- What disc is in a drive, and the drive's makemkvcon name (used for ignored
  drives): `makemkvcon info` isolated to that one drive, under its `Do`, run
  when the drive appears and when a disc arrives.
- Media arriving/leaving: the physical watcher (below).
- Without isolation: every `Do` also takes one global token, so everything is
  serialised as before November. Identify then waits for other drives' work.

### 4. Enforced by a test

`internal/drives/boundary_test.go` parses every non-test Go file outside
`internal/drives` and its implementation packages (`internal/makemkv`,
`internal/mpls`, `internal/ddrescue`) and fails on: string literals containing
`/dev/sr` or `/dev/sg`; `exec.Command*` of blkid, mount, umount, ddrescue or
makemkvcon; references to `makemkv.NewExecutor`, `mpls.OpenDiscRoot`,
`mpls.ForceUnmount`, `mpls.ReadDiscLanguages`, `ddrescue.Rescue`; and opening
device nodes with `unix.Open`. `main.go` gets no exemption.

### 5. The one exception: the eject watcher

Opens each drive O_NONBLOCK and asks CDROM_DRIVE_STATUS once a second. The
kernel sends the drive TEST UNIT READY / GET EVENT STATUS for it. It runs beside
a holder on purpose: it is what catches a disc pulled mid-rip. It lives inside
`internal/drives`. If a hang ever follows one landing mid-command, it moves
under the lease too.

## What goes away

`driveLocks`, `Executor.claim`, `ClaimDrive`, `ListDrives`/`TryListDrives`,
the remembered-listing fallback, `DevicePathForDrive`, the blkid labeler, the
listing debounces (`ejectConfirmDuration`, `driveGoneConfirmDuration`), and
every disc mount made outside a holder.

## Known costs

- An ignored drive is touched once, by an isolated `makemkvcon info` under its
  own lane, to learn its name. It was touched every five seconds before.
- Drive indexes become srN. Rows and backups recorded under the old makemkvcon
  index may name a different drive; a backup bound to the wrong drive is
  unbound by `SetDriveDisc` when the labels differ.
