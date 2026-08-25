ALTER TABLE unmanaged_releases ADD COLUMN album_artist TEXT NOT NULL DEFAULT '';
ALTER TABLE unmanaged_releases ADD COLUMN album_title TEXT NOT NULL DEFAULT '';
ALTER TABLE unmanaged_releases ADD COLUMN release_year TEXT NOT NULL DEFAULT '';
ALTER TABLE unmanaged_releases ADD COLUMN album_artist_normalized TEXT NOT NULL DEFAULT '';
ALTER TABLE unmanaged_releases ADD COLUMN album_title_normalized TEXT NOT NULL DEFAULT '';
ALTER TABLE unmanaged_releases ADD COLUMN path_basename_normalized TEXT NOT NULL DEFAULT '';

UPDATE unmanaged_releases
SET album_artist = trim(COALESCE(json_extract(approved_plan_json, '$.metadata.album_artist'), '')),
    album_title = trim(COALESCE(json_extract(approved_plan_json, '$.metadata.album'), '')),
    release_year = substr(trim(COALESCE(json_extract(approved_plan_json, '$.metadata.date'), '')), 1, 4),
    album_artist_normalized = lower(trim(COALESCE(json_extract(approved_plan_json, '$.metadata.album_artist'), ''))),
    album_title_normalized = lower(trim(COALESCE(json_extract(approved_plan_json, '$.metadata.album'), '')));

WITH RECURSIVE path_parts(candidate_id, rest, part) AS (
    SELECT candidate_id, replace(COALESCE(final_path, ''), char(92), '/') || '/', ''
    FROM unmanaged_releases
    UNION ALL
    SELECT candidate_id, substr(rest, instr(rest, '/') + 1), substr(rest, 1, instr(rest, '/') - 1)
    FROM path_parts
    WHERE rest <> ''
), basenames(candidate_id, basename) AS (
    SELECT candidate_id, lower(trim(part)) FROM path_parts WHERE rest = ''
)
UPDATE unmanaged_releases
SET path_basename_normalized = COALESCE((SELECT basename FROM basenames WHERE basenames.candidate_id = unmanaged_releases.candidate_id), '');

DROP INDEX unmanaged_releases_status_updated;
CREATE INDEX unmanaged_status_updated ON unmanaged_releases(status, updated_at DESC, candidate_id DESC);
CREATE INDEX unmanaged_updated ON unmanaged_releases(updated_at DESC, candidate_id DESC);
CREATE INDEX unmanaged_artist_search ON unmanaged_releases(album_artist_normalized, candidate_id);
CREATE INDEX unmanaged_album_search ON unmanaged_releases(album_title_normalized, candidate_id);
CREATE INDEX unmanaged_path_search ON unmanaged_releases(path_basename_normalized, candidate_id);
