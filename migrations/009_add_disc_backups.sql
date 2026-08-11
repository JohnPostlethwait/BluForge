-- Scratch disc backups taken during spurious-AACS recovery.
--
-- These are up to ~100GB each and take tens of minutes to produce, so they must
-- survive a restart: losing the record would both orphan the copy on disk and
-- send the next scan back to a drive MakeMKV cannot read. The startup sweep uses
-- this table to tell a live backup from crash debris.
CREATE TABLE IF NOT EXISTS disc_backups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    drive_index INTEGER NOT NULL,
    disc_label  TEXT NOT NULL DEFAULT '',
    -- Absolute path of the backup directory; unique so re-recovering the same
    -- disc replaces its row rather than accumulating duplicates.
    backup_dir  TEXT NOT NULL UNIQUE,
    -- The makemkvcon source argument, e.g. "file:/output/.bluforge-scratch/x".
    source_arg  TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_disc_backups_drive ON disc_backups(drive_index);
