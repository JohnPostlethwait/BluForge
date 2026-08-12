-- size_bytes is the estimate MakeMKV made for the title on the disc, captured
-- when the job is created. It was displayed against a completed rip as though
-- it described the delivered file, which reported a 67.4 GB success for a file
-- that is 118 MB on disk.
--
-- output_size_bytes is the file that actually landed, measured after the move.
-- Zero means not yet recorded, which is every job that predates this column.
ALTER TABLE rip_jobs ADD COLUMN output_size_bytes INTEGER NOT NULL DEFAULT 0;
