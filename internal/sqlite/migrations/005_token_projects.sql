CREATE TABLE api_token_projects (
    token_id TEXT NOT NULL,
    project TEXT NOT NULL CHECK (length(trim(project)) > 0),
    PRIMARY KEY (token_id, project),
    FOREIGN KEY (token_id) REFERENCES api_tokens(id)
);
