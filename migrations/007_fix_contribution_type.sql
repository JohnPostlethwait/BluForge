-- 007_fix_contribution_type.sql
-- Fix contribution_type for existing update contributions that were incorrectly
-- defaulted to 'add' when migration 005 added the column.
-- Update contributions are identified by having a non-empty match_info.
UPDATE contributions
SET contribution_type = 'update'
WHERE match_info != ''
  AND contribution_type != 'update';
