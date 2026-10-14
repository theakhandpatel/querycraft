-- Querycraft PostgreSQL bootstrap (runs once, on first cluster init).
--
-- One server, one database per environment:
--
--   querycraft       dev / preview
--   querycraft_test  the gated integration tests (QC_TEST_DATABASE_URL)
--
-- Inside every database two schemas separate concerns:
--
--   dataset   learner-visible commerce tables (customers, products, orders,
--             order_items, dataset_meta). A restricted execution role can be
--             granted SELECT on this schema alone.
--   app       gateway metadata (progress, events, content_docs). Never on the
--             learner search_path.
--
-- The applications create both schemas themselves (schema.sql for `dataset`,
-- the store migration for `app`), so a brand-new database works from zero.
--
-- POSTGRES_DB already creates `querycraft`; the guards below make this script
-- idempotent and also create `querycraft_test` on the same server.

\set ON_ERROR_STOP on

SELECT 'CREATE DATABASE querycraft OWNER querycraft'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'querycraft')\gexec

SELECT 'CREATE DATABASE querycraft_test OWNER querycraft'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'querycraft_test')\gexec
