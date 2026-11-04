-- When MakeMKV started and finished writing the title. A job's created_at and
-- updated_at also cover the queue, the analysis before the copy, and the move
-- afterwards, so they cannot give the copy's throughput.
--
-- NULL means not recorded: every job that predates these columns, and any job
-- whose copy never started or did not finish.
ALTER TABLE rip_jobs ADD COLUMN copy_started_at DATETIME;
ALTER TABLE rip_jobs ADD COLUMN copy_finished_at DATETIME;
