-- Per-disc record of how a disc was read and which rip path it took.
--
-- Discs whose AACS directory is spurious are scattered and unpredictable —
-- three of twenty-five in one box set, across non-contiguous seasons. A row per
-- scan is what makes a recurrence diagnosable without repeating the whole
-- investigation, and knowing which discs took the direct path is part of that.
CREATE TABLE IF NOT EXISTS disc_diagnostics (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    disc_label        TEXT NOT NULL,
    -- Filled in only after a successful scan: the disc key hashes the title
    -- list, so it degrades to a useless constant when a scan returned nothing.
    disc_key          TEXT NOT NULL DEFAULT '',
    drive_index       INTEGER NOT NULL DEFAULT 0,
    -- Parsed from the .tgz dump MakeMKV writes, e.g. MKB20_v82_<title>_<hash>.tgz
    mkb_version       TEXT NOT NULL DEFAULT '',
    aacs_dir_present  INTEGER NOT NULL DEFAULT 0,
    -- unencrypted | scrambled | unknown | n/a
    scramble_verdict  TEXT NOT NULL DEFAULT '',
    packets_checked   INTEGER NOT NULL DEFAULT 0,
    scrambled_packets INTEGER NOT NULL DEFAULT 0,
    -- direct | backup_strip | blocked
    rip_path          TEXT NOT NULL DEFAULT '',
    -- ok | failed
    outcome           TEXT NOT NULL DEFAULT '',
    detail            TEXT NOT NULL DEFAULT '',
    dump_path         TEXT NOT NULL DEFAULT '',
    backup_bytes      INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_disc_diagnostics_label ON disc_diagnostics(disc_label);
CREATE INDEX IF NOT EXISTS idx_disc_diagnostics_created ON disc_diagnostics(created_at);
