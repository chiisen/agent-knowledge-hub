CREATE TABLE knowledge (
    id TEXT PRIMARY KEY NOT NULL,
    title TEXT NOT NULL CHECK (length(trim(title)) > 0),
    content TEXT NOT NULL CHECK (length(trim(content)) > 0),
    type TEXT NOT NULL CHECK (length(trim(type)) > 0),
    scope TEXT NOT NULL CHECK (length(trim(scope)) > 0),
    project TEXT,
    module TEXT,
    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'confirmed', 'deprecated')),
    source TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
