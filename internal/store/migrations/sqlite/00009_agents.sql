-- +goose Up
-- Host agents (Linux, Proxmox) that report byte counters per MAC. Each agent
-- has exactly one API token with the scope agent:report; revoking the token
-- disables the agent.
CREATE TABLE agents (
    token_id    INTEGER PRIMARY KEY REFERENCES api_tokens(id) ON DELETE CASCADE,
    hostname    TEXT    NOT NULL DEFAULT '',
    mode        TEXT    NOT NULL DEFAULT '',
    version     TEXT    NOT NULL DEFAULT '',
    remote_ip   TEXT    NOT NULL DEFAULT '',
    interfaces  INTEGER NOT NULL DEFAULT 0,
    last_report INTEGER,
    created_at  INTEGER NOT NULL
);

-- +goose Down
DROP TABLE agents;
