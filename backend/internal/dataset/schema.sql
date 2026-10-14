-- Querycraft commerce dataset: schema.
-- Generated dataset_version: 1. Schema and seed share one dataset_version.
-- Rebuild with: make dataset (regenerates schema.sql and seed.sql).

BEGIN;

CREATE SCHEMA IF NOT EXISTS "dataset";

DROP TABLE IF EXISTS "dataset"."order_items";
DROP TABLE IF EXISTS "dataset"."orders";
DROP TABLE IF EXISTS "dataset"."products";
DROP TABLE IF EXISTS "dataset"."customers";
DROP TABLE IF EXISTS "dataset"."dataset_meta";

CREATE TABLE "dataset"."customers" (
    "id"          integer PRIMARY KEY,
    "name"        text    NOT NULL,
    "email"       text,
    "city"        text,
    "signup_date" date    NOT NULL
);

CREATE TABLE "dataset"."products" (
    "id"       integer       PRIMARY KEY,
    "name"     text          NOT NULL,
    "category" text          NOT NULL,
    "price"    numeric(10,2) NOT NULL,
    "stock"    integer       NOT NULL DEFAULT 0,
    "active"   boolean       NOT NULL DEFAULT true
);

CREATE TABLE "dataset"."orders" (
    "id"          integer   PRIMARY KEY,
    "customer_id" integer   NOT NULL REFERENCES "dataset"."customers"("id"),
    "ordered_at"  timestamp NOT NULL,
    "status"      text      NOT NULL
);

CREATE TABLE "dataset"."order_items" (
    "id"         integer       PRIMARY KEY,
    "order_id"   integer       NOT NULL REFERENCES "dataset"."orders"("id"),
    "product_id" integer       NOT NULL REFERENCES "dataset"."products"("id"),
    "quantity"   integer       NOT NULL CHECK (quantity > 0),
    "unit_price" numeric(10,2) NOT NULL
);

CREATE INDEX "orders_customer_id_idx" ON "dataset"."orders" ("customer_id");
CREATE INDEX "orders_status_idx" ON "dataset"."orders" ("status");
CREATE INDEX "order_items_order_id_idx" ON "dataset"."order_items" ("order_id");
CREATE INDEX "order_items_product_id_idx" ON "dataset"."order_items" ("product_id");

CREATE TABLE "dataset"."dataset_meta" (
    "dataset" text    PRIMARY KEY,
    "version" integer NOT NULL
);

INSERT INTO "dataset"."dataset_meta" ("dataset", "version") VALUES ('commerce', 1);

COMMIT;
