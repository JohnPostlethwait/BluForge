CREATE TABLE IF NOT EXISTS rip_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    drive_index INTEGER NOT NULL,
    disc_name TEXT NOT NULL,
    title_index INTEGER NOT NULL,
    title_name TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT '',
    output_path TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    progress INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    duration TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS disc_mappings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    disc_key TEXT NOT NULL UNIQUE,
    disc_name TEXT NOT NULL,
    media_item_id TEXT NOT NULL,
    release_id TEXT NOT NULL,
    media_title TEXT NOT NULL DEFAULT '',
    media_year TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS discdb_cache (
    cache_key TEXT PRIMARY KEY,
    response_json TEXT NOT NULL,
    expires_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE INDEX IF NOT EXISTS idx_rip_jobs_status ON rip_jobs(status);
CREATE INDEX IF NOT EXISTS idx_rip_jobs_created ON rip_jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_disc_mappings_disc_key ON disc_mappings(disc_key);
CREATE INDEX IF NOT EXISTS idx_discdb_cache_expires ON discdb_cache(expires_at);
