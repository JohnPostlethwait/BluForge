-- 006_unify_contribution_metadata.sql
-- For update contributions, copy asin/release_date/front_image_url from match_info
-- into release_info, then strip them from match_info.
-- This unifies storage so ASIN/date/image always live in release_info.

UPDATE contributions
SET release_info = json_set(
      CASE WHEN release_info = '' THEN '{}' ELSE release_info END,
      '$.asin', json_extract(match_info, '$.asin'),
      '$.release_date', json_extract(match_info, '$.release_date'),
      '$.front_image_url', json_extract(match_info, '$.front_image_url')
    ),
    match_info = json_remove(match_info, '$.asin', '$.release_date', '$.front_image_url')
WHERE contribution_type = 'update'
  AND match_info != ''
  AND (
    json_extract(match_info, '$.asin') IS NOT NULL
    OR json_extract(match_info, '$.release_date') IS NOT NULL
    OR json_extract(match_info, '$.front_image_url') IS NOT NULL
  );
