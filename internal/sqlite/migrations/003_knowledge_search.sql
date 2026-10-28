CREATE VIRTUAL TABLE knowledge_fts USING fts5(
    id UNINDEXED,
    title,
    content,
    content='knowledge',
    content_rowid='rowid',
    tokenize='trigram'
);

CREATE TRIGGER knowledge_fts_insert AFTER INSERT ON knowledge BEGIN
    INSERT INTO knowledge_fts(rowid, id, title, content)
    VALUES (new.rowid, new.id, new.title, new.content);
END;

CREATE TRIGGER knowledge_fts_update AFTER UPDATE OF title, content ON knowledge BEGIN
    INSERT INTO knowledge_fts(knowledge_fts, rowid, id, title, content)
    VALUES ('delete', old.rowid, old.id, old.title, old.content);
    INSERT INTO knowledge_fts(rowid, id, title, content)
    VALUES (new.rowid, new.id, new.title, new.content);
END;

CREATE TRIGGER knowledge_fts_delete AFTER DELETE ON knowledge BEGIN
    INSERT INTO knowledge_fts(knowledge_fts, rowid, id, title, content)
    VALUES ('delete', old.rowid, old.id, old.title, old.content);
END;

INSERT INTO knowledge_fts(knowledge_fts) VALUES ('rebuild');
