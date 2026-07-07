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
    token TEXT,              -- registration token (standalone)
    dir TEXT,                -- runner directory (standalone)
    pat TEXT,                -- personal access token (scaleset)
    scale_set_name TEXT,     -- scaleset name (scaleset)
    max_runners INTEGER DEFAULT 0,
    labels TEXT DEFAULT '',  -- comma-separated
    runner_group TEXT DEFAULT 'Default',
    jobs_completed INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
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
