-- Click aggregation table.
-- Worker upserts into this on each batch flush; one row per code with a
-- running count. For richer analytics (geo, ua, time buckets) we'd use
-- an OLAP store like ClickHouse — Postgres is fine for v1.
CREATE TABLE IF NOT EXISTS clicks (
    code        VARCHAR(16)  PRIMARY KEY REFERENCES links(code) ON DELETE CASCADE,
    count       BIGINT       NOT NULL DEFAULT 0,
    last_click  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS clicks_last_click_idx ON clicks (last_click DESC);
