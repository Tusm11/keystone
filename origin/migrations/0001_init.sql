-- Keystone initial schema.
-- Runs automatically on first postgres container boot via docker-entrypoint-initdb.d.

CREATE TABLE IF NOT EXISTS links (
    code        VARCHAR(16)  PRIMARY KEY,
    long_url    TEXT         NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Index on created_at for cheap "newest first" listings and TTL sweeps later.
CREATE INDEX IF NOT EXISTS links_created_at_idx ON links (created_at DESC);
