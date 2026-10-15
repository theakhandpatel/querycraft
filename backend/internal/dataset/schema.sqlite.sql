-- Querycraft commerce dataset: schema (SQLite dialect).
-- dataset_version: 1 — the SQLite mirror of schema.sql.
--
-- The same four relations (customers, products, orders, order_items) plus
-- dataset_meta, with the PostgreSQL declared types but none of PostgreSQL's
-- guarantees that SQLite does not have:
--
--   * no schemas: the tables are unqualified and there is no search_path. The
--     PostgreSQL `dataset` schema has no SQLite counterpart, which is why the
--     four relations of this dialect are what a lesson can address unqualified.
--   * no roles: authentication and read-only enforcement are the host's job.
--   * a declared type names an affinity, not a constraint. `DATE` and
--     `TIMESTAMP` select NUMERIC affinity and the seed stores ISO-8601 text in
--     them (measured: typeof(signup_date) = 'text'), and `BOOLEAN` stores 1/0.
--     That is SQLite's dynamic typing, documented in docs/sqlite-dialect.md and
--     mapped column by column in mapping.json.
--
-- `INTEGER PRIMARY KEY` is the rowid alias, which is the whole identity story
-- here; AUTOINCREMENT is not used (the seed supplies explicit ids, and
-- AUTOINCREMENT would only add a sqlite_sequence table and forbid id reuse).
--
-- Checked by: node scripts/dataset-map-audit.mjs

BEGIN;

DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS customers;
DROP TABLE IF EXISTS dataset_meta;

CREATE TABLE customers (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL,
    email       TEXT,
    city        TEXT,
    signup_date DATE    NOT NULL
);

CREATE TABLE products (
    id       INTEGER PRIMARY KEY,
    name     TEXT    NOT NULL,
    category TEXT    NOT NULL,
    price    NUMERIC NOT NULL,
    stock    INTEGER NOT NULL DEFAULT 0,
    active   BOOLEAN NOT NULL DEFAULT 1
);

CREATE TABLE orders (
    id          INTEGER PRIMARY KEY,
    customer_id INTEGER NOT NULL REFERENCES customers(id),
    ordered_at  TIMESTAMP NOT NULL,
    status      TEXT      NOT NULL
);

CREATE TABLE order_items (
    id         INTEGER PRIMARY KEY,
    order_id   INTEGER NOT NULL REFERENCES orders(id),
    product_id INTEGER NOT NULL REFERENCES products(id),
    quantity   INTEGER NOT NULL CHECK (quantity > 0),
    unit_price NUMERIC NOT NULL
);

CREATE INDEX orders_customer_id_idx ON orders (customer_id);
CREATE INDEX orders_status_idx ON orders (status);
CREATE INDEX order_items_order_id_idx ON order_items (order_id);
CREATE INDEX order_items_product_id_idx ON order_items (product_id);

CREATE TABLE dataset_meta (
    dataset TEXT    PRIMARY KEY,
    version INTEGER NOT NULL
);

INSERT INTO dataset_meta (dataset, version) VALUES ('commerce', 1);

COMMIT;
