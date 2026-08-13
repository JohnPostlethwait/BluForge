-- A salvage records its scratch directory the moment it is created, before it
-- has anything worth ripping, so the startup sweep can tell it from crash
-- debris. Without this a restart mid-salvage deleted the copy: John lost a
-- backup and 11.8GB of rescued data that way, on the first real run.
--
-- Partial marks a copy that is not yet rippable. It is protected from the
-- sweep, but never restored as a disc ready to rip.
ALTER TABLE disc_backups ADD COLUMN partial INTEGER NOT NULL DEFAULT 0;
