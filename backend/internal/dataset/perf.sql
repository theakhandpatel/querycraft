-- Querycraft perf dataset: schema + deterministic seed for plan lessons.
--
-- The shared commerce dataset is deliberately tiny (30 customers, 20 products,
-- 80 orders) so every lesson is fast; at that size the planner correctly chooses
-- a sequential scan and a plan lesson can teach nothing. This is the second,
-- larger fixture the `plans` course runs against: the same four relations and
-- the same grain, at a scale (2000 customers, 500 products, 10000 orders, 20000
-- order items) where the access path is a decision.
--
-- Every row is derived from generate_series arithmetic, never from random(), so
-- the same server version produces byte-identical rows on every machine and a
-- plan captured here is reproducible (M5 exit gate).
--
-- The fixture is pinned to the facts the `plans` course teaches, and
-- `scripts/perf-content-audit.mjs` executes every authored lesson statement
-- against this seed and fails if any of them stops being true:
--
--   orders.customer_id   a skewed key with a real ranking: customer 1 owns 490
--                        orders, 2 owns 430, 3 owns 400, down to 184 for
--                        customer 7 (the worked example in modules 2 and 3) and
--                        152 each for customers 11-50, then a long tail. The
--                        skew is what makes an unfiltered customer report worth
--                        a hash join and `WHERE customer_id = 7` selective.
--   orders.status        five values with a 90% / 0.5% split: shipped 9000,
--                        completed 400, cancelled 300, pending 250, refunded 50.
--                        The same index is pointless for the 90% value and
--                        decisive for the 0.5% value (modules 3 and 4).
--   orders.ordered_at    strictly increasing with the id, so "the newest five
--                        orders" is a determinate answer and a top-N sort is
--                        reproducible.
--   order_items          exactly two lines per order (20000 = 2 x 10000), so
--                        "order 1050 has two lines" is a fact a lesson can
--                        state, and product popularity is skewed by rank so a
--                        "top products" report has a ranking rather than a tie.
--   orders (width)       the relation carries the columns a real order carries
--                        (carrier, tracking code, two addresses, a handling
--                        note), and that is load-bearing, not decoration. An
--                        access-path lesson needs the relation to have enough
--                        pages for the choice to be real: at 10 000 narrow rows
--                        `orders` is ~100 pages, and for a 0.5% predicate a
--                        sequential scan genuinely IS the cheaper plan (~100
--                        sequential pages against ~200 random ones), so the
--                        lesson would be teaching a decision the planner is
--                        right to refuse. These columns put the relation at
--                        roughly two thousand pages, which is the scale modules
--                        3 and 4 describe: the 90% predicate wants a sequential
--                        scan and the 0.5% predicate wants orders_status_idx.
--
-- The two columns a lesson reasons about are seeded from two independent
-- permutations of the row number (status_rank and customer_rank below), so a
-- customer's orders are not correlated with their statuses: every customer sees
-- a mix, as they would in a real order history.
--
-- The index set is part of the lesson: customer_id and status are indexed and
-- ordered_at is not, so "the report by recency table-scans" is a real, fixable
-- observation rather than a claim.

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
    "id"               integer   PRIMARY KEY,
    "customer_id"      integer   NOT NULL REFERENCES "dataset"."customers"("id"),
    "ordered_at"       timestamp NOT NULL,
    "status"           text      NOT NULL,
    "carrier"          text      NOT NULL,
    "tracking_code"    text      NOT NULL,
    "shipping_address" text      NOT NULL,
    "billing_address"  text      NOT NULL,
    "notes"            text      NOT NULL
);

CREATE TABLE "dataset"."order_items" (
    "id"         integer       PRIMARY KEY,
    "order_id"   integer       NOT NULL REFERENCES "dataset"."orders"("id"),
    "product_id" integer       NOT NULL REFERENCES "dataset"."products"("id"),
    "quantity"   integer       NOT NULL CHECK (quantity > 0),
    "unit_price" numeric(10,2) NOT NULL
);

-- The deliberate index set. orders_ordered_at_idx is absent on purpose: it is
-- the index the learner adds in the "most recent orders" lesson.
CREATE INDEX "orders_customer_id_idx" ON "dataset"."orders" ("customer_id");
CREATE INDEX "orders_status_idx" ON "dataset"."orders" ("status");
CREATE INDEX "order_items_order_id_idx" ON "dataset"."order_items" ("order_id");
CREATE INDEX "order_items_product_id_idx" ON "dataset"."order_items" ("product_id");

CREATE TABLE "dataset"."dataset_meta" (
    "dataset" text    PRIMARY KEY,
    "version" integer NOT NULL
);

-- customers: 2000 rows. The first thirty are the same thirty people as the
-- small commerce fixture, so a customer named in one course reads the same in
-- the other; the rest of the base is generated from the id.
INSERT INTO "dataset"."customers" ("id", "name", "email", "city", "signup_date")
SELECT
    g AS id,
    COALESCE(named.name, 'Customer ' || g) AS name,
    CASE WHEN g % 7 = 0 THEN NULL ELSE 'customer' || g || '@example.com' END AS email,
    (ARRAY['Leeds','Milan','Lagos','Osaka','Lisbon','Berlin'])[1 + (g % 6)] AS city,
    DATE '2024-01-01' + ((g * 17) % 730) AS signup_date
FROM generate_series(1, 2000) AS g
LEFT JOIN (VALUES
    (1, 'Ada Whitfield'), (2, 'Bruno Sala'), (3, 'Chidi Okafor'), (4, 'Dara Novak'),
    (5, 'Emi Tanaka'), (6, 'Farid Lindqvist'), (7, 'Gina Rahman'), (8, 'Hiro Ivanov'),
    (9, 'Ines Haidar'), (10, 'Jonas Larsen'), (11, 'Kira Whitfield'), (12, 'Luca Novak'),
    (13, 'Mina Rossi'), (14, 'Noor Berg'), (15, 'Omar Park'), (16, 'Pia Lindqvist'),
    (17, 'Quinn Rahman'), (18, 'Rosa Ivanov'), (19, 'Sami Haidar'), (20, 'Tess Larsen'),
    (21, 'Uma Whitfield'), (22, 'Viktor Novak'), (23, 'Wren Rossi'), (24, 'Xiu Berg'),
    (25, 'Yara Park'), (26, 'Zane Lindqvist'), (27, 'Aya Rahman'), (28, 'Bo Ivanov'),
    (29, 'Cleo Haidar'), (30, 'Dev Larsen')
) AS named(id, name) ON named.id = g;

-- products: 500 rows. Ids 1..20 keep the commerce fixture's catalogue verbatim
-- (so `LIKE 'Aurora%'` and "top products" name real things); 21..500 are
-- generated from the id with a name, a category and a price.
INSERT INTO "dataset"."products" ("id", "name", "category", "price", "stock", "active")
SELECT
    g AS id,
    COALESCE(named.name, (ARRAY['Atlas','Breeze','Cobalt','Delta','Ember','Fjord','Glacier','Halo','Ionic','Juniper','Kestrel','Lumen','Meridian','Nova','Onyx','Pinnacle','Quartz','Ridge','Sable','Tundra'])[1 + (g % 20)]
        || ' ' || (ARRAY['Insulated Bottle','Desk Chair','Wall Clock','Cordless Drill','Water Filter','Charcoal Grill','Garden Hose','Glass Jar','Chef Knife','Floor Lamp','Wall Mirror','Pocket Notebook','Convection Oven','Frying Pan','Wool Rug','Hand Saw','Serving Tray','Umbrella Stand','Hand Vacuum','Socket Wrench'])[1 + ((g / 20) % 20)]) AS name,
    COALESCE(named.category, (ARRAY['Electronics','Home','Outdoor','Kitchen'])[1 + (g % 4)]) AS category,
    COALESCE(named.price, ROUND((20 + (g * 37) % 400)::numeric / 4, 2)) AS price,
    (g * 13) % 60 AS stock,
    (g % 25) <> 0 AS active
FROM generate_series(1, 500) AS g
LEFT JOIN (VALUES
    (1, 'Aurora Wireless Earbuds', 'Electronics', 59.00), (2, 'Nimbus Headphones', 'Electronics', 129.00),
    (3, 'Linen Duvet Cover', 'Home', 84.00), (4, 'Cedar Desk Lamp', 'Home', 42.50),
    (5, 'Trail Runner Jacket', 'Outdoor', 139.00), (6, 'Pulse Fitness Tracker', 'Electronics', 74.25),
    (7, 'Vertex Mechanical Keyboard', 'Electronics', 89.50), (8, 'Harbor Rain Shell', 'Outdoor', 118.00),
    (9, 'Maple Cutting Board', 'Kitchen', 24.00), (10, 'Copper Kettle', 'Kitchen', 65.75),
    (11, 'Wool Throw Blanket', 'Home', 55.00), (12, 'Granite Cookware Set', 'Kitchen', 210.00),
    (13, 'Summit Hiking Poles', 'Outdoor', 96.40), (14, 'Ceramic Vase', 'Home', 33.20),
    (15, 'Espresso Tamper', 'Kitchen', 19.99), (16, 'Solar Camp Lantern', 'Outdoor', 47.00),
    (17, 'Bamboo Shelf', 'Home', 72.30), (18, 'Noise-Cancel Earmuffs', 'Electronics', 64.90),
    (19, 'Rope Dog Leash', 'Outdoor', 15.50), (20, 'Discontinued Camp Stove', 'Outdoor', 0.00)
) AS named(id, name, category, price) ON named.id = g;

-- orders: 10000 rows, two independent permutations of the row number.
--
-- customer_rank gives the skewed key: 490, 430, 400, 360, 340, 320, 184, 156,
-- 132, 108 for customers 1..10, 152 each for 11..50, and 4 each for 51..300.
-- status_rank gives the 90% / 0.5% split across five values. Neither is derived
-- from the other, so a customer's statuses are a genuine mix.
--
-- ordered_at advances 52 minutes per row, so 10 000 orders span
-- 2026-01-01 00:00 .. 2026-12-28 01:48 and the newest order is the highest id.
INSERT INTO "dataset"."orders" ("id", "customer_id", "ordered_at", "status", "carrier", "tracking_code", "shipping_address", "billing_address", "notes")
SELECT
    ranked.n AS id,
    CASE
        WHEN ranked.customer_rank <= 490  THEN 1
        WHEN ranked.customer_rank <= 920  THEN 2
        WHEN ranked.customer_rank <= 1320 THEN 3
        WHEN ranked.customer_rank <= 1680 THEN 4
        WHEN ranked.customer_rank <= 2020 THEN 5
        WHEN ranked.customer_rank <= 2340 THEN 6
        WHEN ranked.customer_rank <= 2524 THEN 7
        WHEN ranked.customer_rank <= 2680 THEN 8
        WHEN ranked.customer_rank <= 2812 THEN 9
        WHEN ranked.customer_rank <= 2920 THEN 10
        WHEN ranked.customer_rank <= 9000 THEN 11 + (ranked.customer_rank - 2921) / 152
        ELSE 51 + (ranked.customer_rank - 9001) / 4
    END AS customer_id,
    TIMESTAMP '2026-01-01 00:00:00' + ((ranked.n - 1) * INTERVAL '52 minutes') AS ordered_at,
    CASE
        WHEN ranked.status_rank <= 9000 THEN 'shipped'
        WHEN ranked.status_rank <= 9400 THEN 'completed'
        WHEN ranked.status_rank <= 9700 THEN 'cancelled'
        WHEN ranked.status_rank <= 9950 THEN 'pending'
        ELSE 'refunded'
    END AS status,
    (ARRAY['DHL','UPS','DPD','GLS','Royal Mail'])[1 + (ranked.n % 5)] AS carrier,
    'QC' || lpad(((ranked.n * 7919) % 100000000)::text, 8, '0') AS tracking_code,
    (ARRAY['1 Harbour Road','22 Kiln Street','9 Foundry Lane','4 Marlow Court','77 Pennant Way','3 Ashcroft Close','15 Weaver Row','60 Ropewalk Street'])[1 + (ranked.n % 8)]
        || ', ' || (ARRAY['Leeds','Milan','Lagos','Osaka','Lisbon','Berlin'])[1 + (ranked.customer_rank % 6)]
        || ' ' || (1000 + (ranked.n * 37) % 8999) AS shipping_address,
    (ARRAY['1 Harbour Road','22 Kiln Street','9 Foundry Lane','4 Marlow Court','77 Pennant Way','3 Ashcroft Close','15 Weaver Row','60 Ropewalk Street'])[1 + ((ranked.n + 3) % 8)]
        || ', ' || (ARRAY['Leeds','Milan','Lagos','Osaka','Lisbon','Berlin'])[1 + ((ranked.customer_rank + 2) % 6)]
        || ' ' || (1000 + (ranked.n * 53) % 8999) AS billing_address,
    'Handling note for order ' || ranked.n || ': '
        || repeat('keep the packaging intact, record the batch number and file the customs form with the courier. ', 16) AS notes
FROM (
    SELECT
        n,
        row_number() OVER (ORDER BY (n * 7919) % 10000) AS status_rank,
        row_number() OVER (ORDER BY (n * 104729) % 10000) AS customer_rank
    FROM generate_series(1, 10000) AS n
) AS ranked;

-- order_items: 20000 rows, exactly two lines per order, so 2 x 10 000 is the
-- 20 000 the fixture reports and an order's line count is a fact a lesson can
-- state. Popularity is skewed by rank with a quadratic decay, so the best
-- selling product owns far more lines than the rest and a "top products"
-- report has a ranking instead of a five-way tie.
INSERT INTO "dataset"."order_items" ("id", "order_id", "product_id", "quantity", "unit_price")
SELECT
    lines.n AS id,
    lines.order_id,
    lines.product_id,
    lines.quantity,
    p.price AS unit_price
FROM (
    SELECT
        ranked.n,
        1 + (ranked.n - 1) / 2 AS order_id,
        1 + FLOOR(POWER((ranked.product_rank - 1)::numeric / 20000, 2) * 500)::int AS product_id,
        1 + (ranked.n % 3) AS quantity
    FROM (
        SELECT n, row_number() OVER (ORDER BY (n * 7919) % 20000) AS product_rank
        FROM generate_series(1, 20000) AS n
    ) AS ranked
) AS lines
JOIN "dataset"."products" p ON p.id = lines.product_id;

INSERT INTO "dataset"."dataset_meta" ("dataset", "version") VALUES ('perf', 1);

-- Statistics travel with the template copy, so a plan captured in an
-- environment is planned from real, per-relation statistics rather than
-- defaults (reltuples = -1), which is what makes the row estimates meaningful.
ANALYZE "dataset"."customers";
ANALYZE "dataset"."products";
ANALYZE "dataset"."orders";
ANALYZE "dataset"."order_items";

COMMIT;
