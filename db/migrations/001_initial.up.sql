-- Settings table for global config
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Runners table
CREATE TABLE runners (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('standalone', 'scaleset')),
    url TEXT NOT NULL,
    token TEXT NOT NULL DEFAULT '',              -- registration token (standalone)
    dir TEXT NOT NULL DEFAULT '',                -- runner directory (standalone)
    pat TEXT NOT NULL DEFAULT '',                -- personal access token (scaleset)
    scale_set_name TEXT NOT NULL DEFAULT '',     -- scaleset name (scaleset)
    max_runners INTEGER NOT NULL DEFAULT 0,
    labels TEXT NOT NULL DEFAULT '',  -- comma-separated
    runner_group TEXT NOT NULL DEFAULT 'Default',
    jobs_completed INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Enterprise domains table
CREATE TABLE enterprise_domains (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    domain TEXT UNIQUE NOT NULL
);

-- Seed default enterprise domain
INSERT INTO enterprise_domains (domain) VALUES ('github.com');

-- Seed default settings
INSERT INTO settings (key, value) VALUES ('max_workers', '5');
INSERT INTO settings (key, value) VALUES ('warm_workers', '0');
