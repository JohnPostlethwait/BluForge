-- A rip from a salvaged disc contains damaged video wherever the disc could not
-- be read. The file itself carries no sign of that: a year from now a glitch at
-- 1:07 has no explanation unless the job that produced it recorded one.
--
-- Empty means an ordinary rip. Salvaged jobs carry a sentence naming how much
-- was unreadable.
ALTER TABLE rip_jobs ADD COLUMN salvage_note TEXT NOT NULL DEFAULT '';
