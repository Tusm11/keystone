-- Enables per-query stats so we can count SELECTs on a specific query
-- (e.g. to prove the singleflight defense).
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

-- Reset function convenience (requires superuser; the keystone user has it in dev).
