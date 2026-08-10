# Troubleshooting

## A disc fails with "The volume key is unknown for this disc"

### The symptom

MakeMKV refuses to open the disc and reports something like:

```
The volume key is unknown for this disc - video can't be decrypted
Failed to open disc
```

No titles are listed. The obvious reading — "MakeMKV is missing this disc's
key, I need to find a key database" — is wrong about half the time, and chasing
it wastes hours.

### What is actually happening

Some retail Blu-ray and UHD discs ship with a complete AACS directory (MKB,
certificates, the whole protection scaffolding) but with the MPEG-TS payload
left **unencrypted**. It is a mastering or replication defect, not a new
protection scheme.

MakeMKV decides whether to run the AACS path purely on the presence of the AACS
directory. When it is present, MakeMKV demands a volume key. For these discs no
such key exists, because nothing was ever encrypted — so it fails, with an error
that describes a completely different problem.

This is scattered and unpredictable. In one investigation it affected 3 of 25
discs in a single box set, across non-contiguous seasons. It cannot be predicted
from the title, the season, or the pressing batch. Discs from the same set, in
the same drive, on the same day will behave differently.

### The failure signature

In `makemkvcon -r` (robot) mode:

| Code | Meaning |
|------|---------|
| `MSG:3303` | The volume key is unknown for this disc - video can't be decrypted |
| `MSG:5010` | Failed to open disc |
| `TCOUNT:0` | No titles found |

**Match on the numeric codes, not the message text.** Text is localized and
changes between releases; codes are stable.

`MSG:5042` ("The program can't find any usable optical drives") is **not** part
of this signature. It appears on nearly every `makemkvcon` invocation including
successful ones, and it is meaningless when the source is a file or folder. It
only matters when the source is `disc:N` — see the drive access section below.

### Telling the two cases apart

**This is the important part.** The failure above is *identical* for a spurious
AACS directory and for a genuinely unknown volume key. You cannot tell from
MakeMKV's output which one you have, and applying the workaround blind means
spending ~100GB and 40 minutes to learn nothing.

The only reliable test is to read the payload and check whether it is actually
scrambled. BluForge does this automatically. To do it by hand:

1. Mount the disc and find the largest `.m2ts` under `BDMV/STREAM/`.
2. Sample several hundred packets from a few offsets — not just the file start.
3. Check the packet structure and scrambling bits.

Two details matter, and both are easy to get wrong:

**Blu-ray `.m2ts` uses a 192-byte stride, not 188.** Each 188-byte transport
stream packet is prefixed with a 4-byte `TP_extra_header` timecode, so the sync
byte `0x47` sits at offset 4 of each 192-byte unit. Code written for plain `.ts`
(188-byte packets, sync at offset 0) finds no sync at all on a real `.m2ts` and
reports "unknown" for every disc, healthy ones included.

**`transport_scrambling_control` is the top two bits of byte 3.**

```
tsc = (packet[3] >> 6) & 0x03      # 00 = clear, 01/10/11 = scrambled
```

Shift first, then mask. Masking with `0x03` before shifting right by 6 always
yields zero, which reads as "never scrambled" — and would send you straight into
the workaround on a disc that genuinely needs a key.

**Sync lock is itself a signal.** AACS encrypts in 6144-byte Aligned Units, of
which only the first 16 bytes stay plaintext. On a genuinely encrypted disc the
sync bytes of packets 1–31 within each unit are themselves ciphertext, so no
192-byte stride will lock at all. What survives is a sync byte at each 6144-byte
boundary plus 4. So:

| Observation | Conclusion |
|---|---|
| Sync locks at 192 across consecutive packets, no scrambling bits set | Unencrypted — the AACS directory is spurious, the workaround applies |
| Sync locks, some scrambling bits set | Genuinely encrypted |
| No stride lock, but `0x47` at each 6144-byte boundary + 4 | Genuinely encrypted (AACS aligned units) |
| Neither | Inconclusive — do **not** apply the workaround |

Treat "inconclusive" exactly like "encrypted". An ambiguous read is not a reason
to spend 100GB on a guess.

### What the AACS directory itself tells you: nothing

Two UHD discs probed side by side — one with a spurious AACS directory, one
that rips normally — carry **identical AACS filenames and near-identical file
sizes**:

```
CPSUnit00001.cci (2048)          ContentRevocation.lst (1048576)
Content000.cer (256)             DH_Pairing_Server.cer (144)
Content001.cer (240)             DUPLICATE (672)
Content002.cer (264)             MKB_RO.inf (5242880)
ContentHash000.tbl (~1.33M)      Unit_Key_RO.inf (65536)
```

Both report the same MKB version (82). The protection metadata is fully
authored on both, which is why this is a replication defect rather than a
truncated authoring step — and why nothing short of reading the payload can
distinguish the two cases.

Note `CPSUnit00001.cci`: content protection is organised into CPS units, so
streams on one disc are not guaranteed to share an encryption state. BluForge
samples the three largest streams and refuses recovery if any of them reads as
encrypted.

### The workaround (spurious AACS only)

BluForge does this automatically when it confirms the payload is unencrypted.
Manually:

```bash
makemkvcon backup disc:0 /path/to/scratch/mydisc
```

Note there is **no `--decrypt` flag**. Decryption is exactly what fails on these
discs; you want a raw copy.

```bash
rm -rf /path/to/scratch/mydisc/AACS
```

```bash
makemkvcon mkv file:/path/to/scratch/mydisc all /path/to/output
```

MakeMKV will now log "AACS directory not present, assuming unencrypted disc" and
complete normally. Budget up to ~100GB of scratch space for a UHD disc, and
check free space *before* you start rather than dying halfway through.

### If the payload really is encrypted

The workaround cannot help — there is no key to be found by removing a
directory. MakeMKV writes a diagnostic dump named like
`MKB20_v82_<title>_<hash>.tgz`. Send it to **svq@makemkv.com** so the key can be
added in a future release. BluForge records this path in its diagnostics and
quotes it in the error.

### What BluForge records

Every disc scan writes a row to the `disc_diagnostics` table: disc label, MKB
version, whether an AACS directory was present, the scrambling verdict, which
rip path was taken (`direct`, `backup_strip`, or `blocked`), and the outcome.

Ordinary discs are recorded too, deliberately. When 3 discs out of 25 misbehave,
knowing that the other 22 took the direct path is half the diagnosis.

---

## Ruling out physical media problems

**A clean `ddrescue` image rules out physical media problems entirely.**

```bash
ddrescue -n -b 2048 /dev/sr0 disc.iso disc.map
```

If it reports 100% rescued with zero bad sectors, the disc surface, the drive,
and the read path are all fine. Whatever you are chasing is a software or
authoring problem, and no amount of cleaning the disc or trying another drive
will change anything.

This check cost an hour during the original investigation. It is documented here
so nobody repeats it unnecessarily — but note it also does not *diagnose*
spurious AACS. A spurious-AACS disc reads perfectly. The clean image only tells
you where **not** to look.

---

## "The program can't find any usable optical drives" (MSG:5042)

### When it matters

`makemkvcon` emits `MSG:5042` on nearly every invocation, including successful
ones, and always when operating on a file or folder source. On its own it means
nothing.

It is only meaningful when **the source is `disc:N` and no drives were
enumerated at all**. BluForge distinguishes these cases and only reports an
error in the second.

### The cause under Docker

`makemkvcon` enumerates drives through `/dev/sg*` nodes. These are mode `0660`
and owned by `root:disk` (commonly GID 6). A container process running as a
non-root user — BluForge defaults to `99:100` on Unraid — that is not in that
group sees **no drives at all**.

The visible symptoms are `MSG:5042` and, on a backup, a bare "Backup failed"
that says nothing about permissions.

### The fix

Add the owning group as a supplementary group:

```yaml
services:
  bluforge:
    group_add:
      - "6"
```

BluForge's `entrypoint.sh` detects the GID owning `/dev/sr*` and `/dev/sg*` and
adds it automatically, so this is usually only needed if you have overridden the
entrypoint or are running the binary outside the image. BluForge also logs a
diagnosis at startup naming the owning group and the identity it is running as.

---

## Files land in the output directory but cannot be renamed or deleted over SMB

Directories were previously created at `0755` while files landed at `0666`. On
Unraid (umask `000`) the SMB group gets no write bit on the directory, so
authenticated users can read `.mkv` files but cannot rename, delete, or
write-temp-then-rename inside the directory.

Output directories are now created with mode `0777`, which under Unraid's
permissive umask yields `0777` — matching the `0666` files and giving
`nobody:users` full write access. On a standard development machine (umask
`022`) the effective mode remains `0755`, unchanged.

If you have directories created by an older version, fix them in place:

```bash
find /path/to/output -type d -exec chmod 0777 {} +
```
