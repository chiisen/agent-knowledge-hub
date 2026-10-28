ALTER TABLE knowledge ADD COLUMN version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0);
ALTER TABLE knowledge ADD COLUMN archived_at INTEGER;

CREATE INDEX knowledge_active_status_created
    ON knowledge(status, created_at DESC, id DESC) WHERE archived_at IS NULL;
CREATE INDEX knowledge_active_project_module
    ON knowledge(project, module) WHERE archived_at IS NULL;
