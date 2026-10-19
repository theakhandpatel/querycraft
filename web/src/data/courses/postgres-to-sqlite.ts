import type { CourseModule } from '../../types';

export const POSTGRES_TO_SQLITE_MODULES: CourseModule[] = [
{ id:'x1', n:1, title:'Casts are gone: :: \u2192 CAST', kind:'Core skill', minutes:14, dialect:'sqlite',
  summary:'There is no ::, CAST applies an affinity instead of checking a type, and DATE_TRUNC becomes a format string over ISO-8601 text.',
  objectives:['Port a :: cast to CAST','Explain why CAST(x AS DATE) is not the fix','Port DATE_TRUNC(\'month\', d)::date to strftime'],
  blocks:[
    {type:'syntax', sql:"SELECT strftime('%Y-%m-01', ordered_at) AS month,\n       CAST(unit_price AS REAL)        AS unit_price_real\nFROM order_items\nORDER BY id\nLIMIT 3;", note:'The port of DATE_TRUNC(\'month\', o.ordered_at)::date is strftime(\'%Y-%m-01\', o.ordered_at): the truncation is a format string, because there is no date type for CAST to aim at.'},
    {type:'example', id:'ex.x1.demo', sql:"SELECT CAST('2026-05-01' AS TEXT) AS text_cast,\n       CAST('7' AS INTEGER)        AS int_cast,\n       CAST(7 AS TEXT)             AS text_from_int,\n       CAST('abc' AS INTEGER)      AS bad_text;", note:'A cast converts, it does not validate. PostgreSQL rejects \'abc\'::integer with 22P02; here it is 0, and a bad value has quietly become a plausible number.',
      result:{columns:['text_cast','int_cast','text_from_int','bad_text'], rows:[['2026-05-01',7,'7',0]]}},
    {type:'exercise', id:'ex.x1.1',
      prompt:'Port the PostgreSQL projection ordered_at::date. Return id, the order\u2019s date as ordered_on and its status for the three earliest orders, oldest first and ties by id.',
      starter:"SELECT id,\n       -- the date part of ordered_at\n       AS ordered_on,\n       status\nFROM orders\n-- oldest first, ties by id\nLIMIT 3;",
      solution:"SELECT id,\n       date(ordered_at) AS ordered_on,\n       status\nFROM orders\nORDER BY ordered_at, id\nLIMIT 3;",
      hints:['<span class="kbd-inline">date(ordered_at)</span> is the port of <span class="kbd-inline">ordered_at::date</span>; there is nothing to cast to.','Add <span class="kbd-inline">id</span> as a second sort key so two orders at the same timestamp have a fixed order.'],
      contract:{columns:['id','ordered_on','status'], column_order:'exact', row_order:'exact'},
      result:{columns:['id','ordered_on','status'], rows:[[1001,'2026-01-04','completed'],[1006,'2026-01-09','shipped'],[1007,'2026-01-11','refunded']]}}
  ]},

{ id:'x2', n:2, title:'Conditional aggregation without FILTER assumptions', kind:'Core skill', minutes:15, dialect:'sqlite',
  summary:'FILTER arrived in 3.30 and DISTINCT ON never will; the portable spellings are a CASE sum and a partitioned row number.',
  objectives:['Place the FILTER version gate correctly','Write the CASE-sum spelling that runs on any build','Port DISTINCT ON with a window function'],
  blocks:[
    {type:'syntax', sql:"SELECT COUNT(*) FILTER (WHERE status = 'completed') AS completed\nFROM orders;", note:'Real SQLite since 3.30 (2019), so this is a version floor rather than a missing feature. Below that floor the CASE sum is the same query.'},
    {type:'example', id:'ex.x2.demo', sql:"SELECT SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) AS completed,\n       SUM(CASE WHEN status = 'cancelled' THEN 1 ELSE 0 END) AS cancelled,\n       SUM(CASE WHEN status = 'refunded'  THEN 1 ELSE 0 END) AS refunded,\n       SUM(CASE WHEN status = 'shipped'   THEN 1 ELSE 0 END) AS shipped,\n       SUM(CASE WHEN status = 'pending'   THEN 1 ELSE 0 END) AS pending\nFROM orders;", note:'Five statuses, one pass, one row: a comparison inside CASE, summed. The ELSE 0 is not decoration \u2014 without it an empty slice sums to NULL.',
      result:{columns:['completed','cancelled','refunded','shipped','pending'], rows:[[37,11,11,11,10]]}},
    {type:'exercise', id:'ex.x2.1',
      prompt:'PostgreSQL\u2019s SELECT DISTINCT ON (customer_id) customer_id, id, ordered_at::date FROM orders ORDER BY customer_id, ordered_at DESC, id DESC has no SQLite spelling. Port it for customers 1 to 5: return customer_id, order_id and ordered_on, one row per customer who has ordered, customers ascending.',
      starter:"WITH ranked AS (\n  SELECT customer_id, id,\n         date(ordered_at) AS ordered_on,\n         -- number this customer\u2019s orders\n         AS rn\n  FROM orders\n  WHERE customer_id <= 5\n)\nSELECT customer_id, id AS order_id, ordered_on\nFROM ranked\nWHERE rn = 1\nORDER BY customer_id;",
      solution:"WITH ranked AS (\n  SELECT customer_id, id,\n         date(ordered_at) AS ordered_on,\n         ROW_NUMBER() OVER (PARTITION BY customer_id ORDER BY ordered_at DESC, id DESC) AS rn\n  FROM orders\n  WHERE customer_id <= 5\n)\nSELECT customer_id, id AS order_id, ordered_on\nFROM ranked\nWHERE rn = 1\nORDER BY customer_id;",
      hints:['<span class="kbd-inline">DISTINCT ON</span> is a PostgreSQL extension and does not parse here at all.','<span class="kbd-inline">ROW_NUMBER() OVER (PARTITION BY customer_id ORDER BY ...)</span> is the replacement, keeping the same tie-break the PostgreSQL ORDER BY carried.','<span class="kbd-inline">RANK()</span> would let a tie through as two rows; <span class="kbd-inline">ROW_NUMBER()</span> keeps exactly one.'],
      contract:{columns:['customer_id','order_id','ordered_on'], column_order:'exact', row_order:'exact'},
      result:{columns:['customer_id','order_id','ordered_on'], rows:[[1,1005,'2026-02-11'],[2,1003,'2026-01-22'],[3,1004,'2026-02-02'],[5,1072,'2026-05-02']]}}
  ]},

{ id:'x3', n:3, title:'Windows and ordering semantics', kind:'Core skill', minutes:14, dialect:'sqlite',
  summary:'The window syntax ports unchanged since 3.25, but NULL ordering defaults are the opposite of PostgreSQL\u2019s and the default frame is a RANGE.',
  objectives:['Say where NULLs sort by default in each engine','Pin the order with NULLS FIRST / NULLS LAST','Tell a RANGE frame from a ROWS frame and choose deliberately'],
  blocks:[
    {type:'syntax', sql:"SELECT category, name, price,\n       RANK() OVER (PARTITION BY category ORDER BY price DESC) AS rk\nFROM products\nORDER BY category, price DESC;", note:'The same OVER shape PostgreSQL uses, available since 3.25. What needs porting is not the syntax but the ordering defaults underneath it.'},
    {type:'example', id:'ex.x3.demo', sql:"SELECT (SELECT city FROM customers ORDER BY city ASC LIMIT 1)               AS first_asc,\n       (SELECT city FROM customers ORDER BY city ASC NULLS LAST LIMIT 1)    AS first_asc_nulls_last,\n       (SELECT city FROM customers ORDER BY city DESC LIMIT 1)              AS first_desc;", note:'The trap in one row: SQLite sorts NULLs first ascending and last descending \u2014 the opposite of PostgreSQL, where the same three subqueries answer Austin, Austin, NULL.',
      result:{columns:['first_asc','first_asc_nulls_last','first_desc'], rows:[[null,'Austin','Toronto']]}},
    {type:'exercise', id:'ex.x3.1',
      prompt:'Port a running total. For each month of completed revenue, return month (the first of the month), the month\u2019s revenue rounded to two decimals, and running_revenue \u2014 the sum of revenue so far including this month. Months ascending.',
      starter:"WITH monthly AS (\n  SELECT strftime('%Y-%m-01', o.ordered_at) AS month,\n         ROUND(SUM(oi.quantity * oi.unit_price), 2) AS revenue\n  FROM orders o\n  JOIN order_items oi ON oi.order_id = o.id\n  WHERE o.status = 'completed'\n  GROUP BY month\n)\nSELECT month, revenue,\n       -- the running sum over the ordered months\n       AS running_revenue\nFROM monthly\nORDER BY month;",
      solution:"WITH monthly AS (\n  SELECT strftime('%Y-%m-01', o.ordered_at) AS month,\n         ROUND(SUM(oi.quantity * oi.unit_price), 2) AS revenue\n  FROM orders o\n  JOIN order_items oi ON oi.order_id = o.id\n  WHERE o.status = 'completed'\n  GROUP BY month\n)\nSELECT month, revenue,\n       ROUND(SUM(revenue) OVER (ORDER BY month), 2) AS running_revenue\nFROM monthly\nORDER BY month;",
      hints:['The window runs over the already-grouped month list, so one row per month is still the grain.','<span class="kbd-inline">SUM(revenue) OVER (ORDER BY month)</span> is the running total; the outer <span class="kbd-inline">ROUND(..., 2)</span> is what keeps float noise out of the result.','If the months could repeat, the default <span class="kbd-inline">RANGE</span> frame would include every peer of the current row \u2014 an explicit <span class="kbd-inline">ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW</span> says which one you mean.'],
      contract:{columns:['month','revenue','running_revenue'], column_order:'exact', row_order:'exact'},
      result:{columns:['month','revenue','running_revenue'], rows:[['2026-01-01',2132.55,2132.55],['2026-02-01',2311.07,4443.62],['2026-03-01',2456.4,6900.02],['2026-04-01',1803.62,8703.64],['2026-05-01',1537.95,10241.59]]}}
  ]},

{ id:'x4', n:4, title:'Dates and intervals without types', kind:'Core skill', minutes:15, dialect:'sqlite',
  summary:'DATE literals and INTERVAL arithmetic do not exist, months normalise instead of clamping, and generate_series is not in the engine.',
  objectives:['Do date arithmetic with modifier strings','Explain why the 31st plus a month is not the 28th','Replace generate_series with a recursive CTE'],
  blocks:[
    {type:'syntax', sql:"SELECT date('2026-01-31', '+1 month')                     AS naive,\n       date('2026-01-31', '+1 month', 'start of month') AS normalised;", note:'The port bug in two values: interval arithmetic is string normalisation, so a month end rolls over rather than clamping. PostgreSQL returns 2026-02-28 for both.'},
    {type:'example', id:'ex.x4.demo', sql:"SELECT date('2026-01-31', '+1 month')                                     AS naive,\n       date('2026-01-31', '+1 month', 'start of month')                   AS normalised,\n       CAST(julianday('2026-02-01') - julianday('2026-01-01') AS INTEGER) AS jan_days;", note:'Thirty-one days between January and February, and the month roll normalised back to the first. julianday() is the dialect\u2019s day arithmetic: a difference of two Julian day numbers.',
      result:{columns:['naive','normalised','jan_days'], rows:[['2026-03-03','2026-03-01',31]]}},
    {type:'exercise', id:'ex.x4.1',
      prompt:'generate_series does not exist, so build the month spine with a recursive CTE: return every month from January to June 2026 as month and how many orders were placed in it as orders, keeping the month that has none. Months ascending.',
      starter:"WITH RECURSIVE months(m) AS (\n  SELECT '2026-01-01'\n  UNION ALL\n  SELECT date(m, '+1 month') FROM months WHERE m < '2026-06-01'\n)\nSELECT months.m AS month,\n       -- count the orders in this month, keeping empty months\n       AS orders\nFROM months\nLEFT JOIN orders ON -- the same month\nGROUP BY months.m\nORDER BY months.m;",
      solution:"WITH RECURSIVE months(m) AS (\n  SELECT '2026-01-01'\n  UNION ALL\n  SELECT date(m, '+1 month') FROM months WHERE m < '2026-06-01'\n)\nSELECT months.m AS month,\n       COUNT(orders.id) AS orders\nFROM months\nLEFT JOIN orders\n  ON strftime('%Y-%m', orders.ordered_at) = strftime('%Y-%m', months.m)\nGROUP BY months.m\nORDER BY months.m;",
      hints:['<span class="kbd-inline">LEFT JOIN</span> is what keeps the spine month that has no orders.','Match the months with <span class="kbd-inline">strftime(\'%Y-%m\', ...)</span> on both sides.','Count <span class="kbd-inline">orders.id</span>, not <span class="kbd-inline">*</span>, so a month with no orders counts zero rather than one.'],
      contract:{columns:['month','orders'], column_order:'exact', row_order:'exact'},
      result:{columns:['month','orders'], rows:[['2026-01-01',16],['2026-02-01',19],['2026-03-01',18],['2026-04-01',18],['2026-05-01',9],['2026-06-01',0]]}}
  ]},

{ id:'x5', n:5, title:'Booleans, numerics and the value contract', kind:'Core skill', minutes:13, dialect:'sqlite',
  summary:'TRUE is the integer 1, a NUMERIC column has no fixed scale, REAL arithmetic puts float noise in a result, and serial becomes the rowid.',
  objectives:['Say what TRUE, FALSE and typeof(TRUE) are','Explain why a money column has no fixed scale','Round a floating sum before it becomes a contract','Port serial to INTEGER PRIMARY KEY and say what AUTOINCREMENT adds'],
  blocks:[
    {type:'syntax', sql:'SELECT TRUE AS t, FALSE AS f, typeof(TRUE) AS t_stored;', note:'Both literals parse and neither is a boolean. PostgreSQL returns t/f and calls the column boolean; here TRUE is the integer 1, and a STRICT table will not let you call the column BOOLEAN either.'},
    {type:'example', id:'ex.x5.demo', sql:"SELECT SUM(1.0 * oi.quantity * oi.unit_price)          AS raw_revenue,\n       ROUND(SUM(1.0 * oi.quantity * oi.unit_price), 2) AS rounded_revenue\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nWHERE o.status = 'completed'\n  AND strftime('%Y-%m', o.ordered_at) = '2026-04';", note:'The same April revenue as a PostgreSQL numeric and as a SQLite REAL: the exact decimal, and the binary float a JSON number cannot shorten. Rounding at the edge is what makes the value a contract instead of an artifact.',
      result:{columns:['raw_revenue','rounded_revenue'], rows:[[1803.6200000000001,1803.62]]}},
    {type:'exercise', id:'ex.x5.1',
      prompt:'Count the products by active flag and price the stock in one row: active_products, inactive_products and stock_value \u2014 SUM(price * stock) rounded to two decimals.',
      starter:"SELECT SUM(CASE WHEN active     THEN 1 ELSE 0 END) AS active_products,\n       -- the inactive products\n       AS inactive_products,\n       -- the stock value, rounded\n       AS stock_value\nFROM products;",
      solution:"SELECT SUM(CASE WHEN active     THEN 1 ELSE 0 END) AS active_products,\n       SUM(CASE WHEN NOT active THEN 1 ELSE 0 END) AS inactive_products,\n       ROUND(SUM(price * stock), 2) AS stock_value\nFROM products;",
      hints:['<span class="kbd-inline">active</span> holds 1 or 0, so it is already a truth value to <span class="kbd-inline">CASE WHEN</span>.','<span class="kbd-inline">ROUND(SUM(price * stock), 2)</span> keeps a floating sum from arriving with more digits than a money column would have.'],
      contract:{columns:['active_products','inactive_products','stock_value'], column_order:'exact', row_order:'ignored'},
      result:{columns:['active_products','inactive_products','stock_value'], rows:[[19,1,24949.6]]}}
  ]},

{ id:'x6', n:6, title:'Users, schemas and permissions do not exist', kind:'Orientation', minutes:12, dialect:'sqlite',
  summary:'No roles, no GRANT, no search_path and no second namespace: one file, one catalog, and access decided by the host.',
  objectives:['Name the PostgreSQL identity objects SQLite lacks','Read the whole catalog from pragma_table_list','Say who enforces read-only when there is no privilege model','List the constructs with no SQLite port at all'],
  blocks:[
    {type:'syntax', sql:"SELECT name, type, ncol, wr, strict\nFROM pragma_table_list\nWHERE schema = 'main'\nORDER BY name;", note:'information_schema.tables, pg_class and the shell\u2019s \\dt collapse into one table-valued pragma whose schema column only ever answers main. wr and strict are the WITHOUT ROWID and STRICT flags, per table.'},
    {type:'example', id:'ex.x6.demo', sql:"SELECT name, type, ncol, wr, strict\nFROM pragma_table_list\nWHERE schema = 'main'\nORDER BY name;", note:'Six rows: the five dataset tables plus sqlite_schema itself, which the catalog lists like any other table. Nothing is qualified by a schema name, because there is only one namespace.',
      result:{columns:['name','type','ncol','wr','strict'], rows:[['customers','table',5,0,0],['dataset_meta','table',2,0,0],['order_items','table',5,0,0],['orders','table',4,0,0],['products','table',6,0,0],['sqlite_schema','table',5,0,0]]}},
    {type:'exercise', id:'ex.x6.1',
      prompt:'Count the file\u2019s user objects: the number of dataset tables as tables and the number of indexes as indexes, leaving out SQLite\u2019s own catalog table.',
      starter:"SELECT (SELECT COUNT(*) FROM sqlite_master\n        WHERE type = 'table' AND -- not the catalog itself\n       ) AS tables,\n       (SELECT COUNT(*) FROM sqlite_master\n        WHERE -- indexes only\n       ) AS indexes;",
      solution:"SELECT (SELECT COUNT(*) FROM sqlite_master\n        WHERE type = 'table' AND name NOT LIKE 'sqlite_%') AS tables,\n       (SELECT COUNT(*) FROM sqlite_master\n        WHERE type = 'index') AS indexes;",
      hints:['<span class="kbd-inline">sqlite_master</span> holds tables, indexes and views in one namespace; <span class="kbd-inline">type</span> tells them apart.','The catalog table is named <span class="kbd-inline">sqlite_schema</span>, so the <span class="kbd-inline">sqlite_%</span> pattern leaves it out.'],
      contract:{columns:['tables','indexes'], column_order:'exact', row_order:'ignored'},
      result:{columns:['tables','indexes'], rows:[[5,5]]}}
  ]},

{ id:'x7', n:7, title:'Porting the capstone', kind:'Core skill', minutes:17, dialect:'sqlite',
  summary:'The m8 capstone rebuilt in SQLite: DATE_TRUNC becomes strftime, the CTE chain survives intact, and the result keeps its shape with SQLite values.',
  objectives:['Translate a four-stage CTE chain from PostgreSQL to SQLite','Keep the grain check and the tie-break while porting','Re-check the values, not just the syntax, after the port'],
  blocks:[
    {type:'syntax', sql:"WITH ranked AS (\n    SELECT month, category, net_revenue, buyers,\n           ROW_NUMBER() OVER (PARTITION BY month ORDER BY net_revenue DESC, category) AS rn\n    FROM by_cat\n)\nSELECT month, category, net_revenue, buyers\nFROM ranked\nWHERE rn = 1\nORDER BY month;", note:'The final stage of the capstone, unchanged: the window is identical. Only the things that name a type or a date moved \u2014 DATE_TRUNC to strftime, and ::date to nothing at all.'},
    {type:'example', id:'ex.x7.demo', sql:"WITH lines AS (\n    SELECT strftime('%Y-%m-01', o.ordered_at) AS month,\n           p.category,\n           o.customer_id,\n           oi.quantity * oi.unit_price AS revenue\n    FROM orders o\n    JOIN order_items oi ON oi.order_id = o.id\n    JOIN products p ON p.id = oi.product_id\n    WHERE o.status = 'completed'\n)\nSELECT month, category,\n       ROUND(SUM(revenue), 2) AS net_revenue,\n       COUNT(DISTINCT customer_id) AS buyers\nFROM lines\nWHERE month = '2026-01-01'\nGROUP BY month, category\nORDER BY net_revenue DESC;", note:'The aggregation stage for January: the same four categories and buyers as the PostgreSQL capstone, with revenue arriving as a REAL to be rounded at the edge.',
      result:{columns:['month','category','net_revenue','buyers'], rows:[['2026-01-01','Outdoor',824.4,4],['2026-01-01','Electronics',717.55,3],['2026-01-01','Home',518.6,5],['2026-01-01','Kitchen',72,2]]}},
    {type:'exercise', id:'ex.x7.1',
      prompt:'Port the capstone end to end: for each month, the top category by net revenue from completed orders, with its revenue rounded to two decimals and the number of distinct customers who bought in it. Return month, category, net_revenue and buyers, one row per month, months ascending.',
      starter:"WITH lines AS (\n    SELECT strftime('%Y-%m-01', o.ordered_at) AS month,\n           p.category,\n           o.customer_id,\n           oi.quantity * oi.unit_price AS revenue\n    FROM orders o\n    JOIN order_items oi ON oi.order_id = o.id\n    JOIN products p ON p.id = oi.product_id\n    WHERE o.status = 'completed'\n),\nby_cat AS (\n    SELECT month, category,\n           ROUND(SUM(revenue), 2) AS net_revenue,\n           COUNT(DISTINCT customer_id) AS buyers\n    FROM lines\n    GROUP BY month, category\n)\nSELECT month, category, net_revenue, buyers\nFROM by_cat\n-- one row per month: rank within the month and keep the best\nORDER BY month;",
      solution:"WITH lines AS (\n    SELECT strftime('%Y-%m-01', o.ordered_at) AS month,\n           p.category,\n           o.customer_id,\n           oi.quantity * oi.unit_price AS revenue\n    FROM orders o\n    JOIN order_items oi ON oi.order_id = o.id\n    JOIN products p ON p.id = oi.product_id\n    WHERE o.status = 'completed'\n),\nby_cat AS (\n    SELECT month, category,\n           ROUND(SUM(revenue), 2) AS net_revenue,\n           COUNT(DISTINCT customer_id) AS buyers\n    FROM lines\n    GROUP BY month, category\n),\nranked AS (\n    SELECT month, category, net_revenue, buyers,\n           ROW_NUMBER() OVER (PARTITION BY month ORDER BY net_revenue DESC, category) AS rn\n    FROM by_cat\n)\nSELECT month, category, net_revenue, buyers\nFROM ranked\nWHERE rn = 1\nORDER BY month;",
      hints:['Keep the filter in <span class="kbd-inline">lines</span>, the earliest stage, so the business rule lives in one place.','<span class="kbd-inline">ROW_NUMBER()</span> over the month partition, then <span class="kbd-inline">WHERE rn = 1</span> in the next stage \u2014 a window function cannot be filtered in the SELECT that defines it.','<span class="kbd-inline">ORDER BY net_revenue DESC, category</span> is the tie-break: revenue alone would leave a tie to chance, so two runs could disagree.'],
      contract:{columns:['month','category','net_revenue','buyers'], column_order:'exact', row_order:'exact'},
      result:{columns:['month','category','net_revenue','buyers'], rows:[['2026-01-01','Outdoor',824.4,4],['2026-02-01','Electronics',891.5,4],['2026-03-01','Electronics',868.05,5],['2026-04-01','Electronics',708.55,4],['2026-05-01','Outdoor',589,3]]}}
  ]}
];
