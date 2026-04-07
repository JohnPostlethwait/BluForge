ALTER TABLE contributions ADD COLUMN contribution_type TEXT NOT NULL DEFAULT 'add';
ALTER TABLE contributions ADD COLUMN match_info TEXT NOT NULL DEFAULT '';
