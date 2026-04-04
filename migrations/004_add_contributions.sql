CREATE TABLE IF NOT EXISTS contributions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    disc_key      TEXT NOT NULL UNIQUE,
    disc_name     TEXT NOT NULL,
    raw_output    TEXT NOT NULL,
    scan_json     TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    pr_url        TEXT NOT NULL DEFAULT '',
    tmdb_id       TEXT NOT NULL DEFAULT '',
    release_info  TEXT NOT NULL DEFAULT '',
    title_labels  TEXT NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_contributions_status ON contributions(status);
CREATE INDEX IF NOT EXISTS idx_contributions_disc_key ON contributions(disc_key);
