/* Curriculum + dataset content, lifted verbatim from the OpenDesign prototype
   (index.html lines 1013-1310). This is the course-authoring source of truth. */

import type { Block, Course, CourseModule, TableDef } from '../types';

var TABLES: TableDef[] = [
  {name:'customers', rows:30, cols:[['id','integer','PK'],['name','text',''],['email','text','nullable'],['city','text','nullable'],['signup_date','date','']],
   preview:[['id','name','email','city'],['1','Ada Whitfield','ada@example.com','Leeds'],['2','Bruno Sala','bruno@example.com','Milan'],['3','Chidi Okafor','NULL','Lagos'],['4','Dara Novak','dara@example.com','NULL'],['5','Emi Tanaka','emi@example.com','Osaka']]},
  {name:'products', rows:20, cols:[['id','integer','PK'],['name','text',''],['category','text',''],['price','numeric',''],['stock','integer',''],['active','boolean','']],
   preview:[['id','name','category','price'],['1','Aurora Wireless Earbuds','Electronics','59.00'],['2','Nimbus Headphones','Electronics','129.00'],['3','Linen Duvet Cover','Home','84.00'],['4','Cedar Desk Lamp','Home','42.50'],['5','Trail Runner Jacket','Outdoor','139.00']]},
  {name:'orders', rows:80, cols:[['id','integer','PK'],['customer_id','integer','FK'],['ordered_at','timestamp',''],['status','text','']],
   preview:[['id','customer_id','ordered_at','status'],['1001','1','2026-01-04 10:12','completed'],['1002','1','2026-01-19 16:40','completed'],['1003','2','2026-01-22 09:03','cancelled'],['1004','3','2026-02-02 14:20','completed'],['1005','1','2026-02-11 11:55','completed']]},
  {name:'order_items', rows:160, cols:[['id','integer','PK'],['order_id','integer','FK'],['product_id','integer','FK'],['quantity','integer',''],['unit_price','numeric','']],
   preview:[['id','order_id','product_id','quantity','unit_price'],['1','1001','1','2','59.00'],['2','1001','3','1','84.00'],['3','1002','5','1','139.00'],['4','1004','2','1','129.00'],['5','1005','1','3','59.00']]}
];

/* ---------- course content ---------- */
function intro(p: string, section?: string): Block { return {type:'prose', html:p, section:section||undefined}; }

var MODULES: CourseModule[] = [
{ id:'m1', n:1, title:'Read the data model', kind:'Orientation', minutes:8,
  summary:'Understand table grain, keys, and business rules before writing joins.',
  objectives:['Identify the grain of each table','Know the primary and foreign keys','Filter and project with confidence'],
  blocks:[
    intro('Every query answers a question about a table\u2019s <em>grain</em> \u2014 what one row represents. In this dataset, a row in <span class="kbd-inline">customers</span> is one person, a row in <span class="kbd-inline">orders</span> is one checkout, and a row in <span class="kbd-inline">order_items</span> is one product line inside an order. Get the grain wrong and everything downstream is wrong, quietly.'),
    intro('Naming tells you how the tables connect: <span class="kbd-inline">orders.customer_id</span> points at <span class="kbd-inline">customers.id</span>, and <span class="kbd-inline">order_items.order_id</span> points at <span class="kbd-inline">orders.id</span>. Those are the join keys you will use all course.'),
    {type:'syntax', sql:'-- one row per product\nSELECT id, name, category, price\nFROM products\nORDER BY price;', note:'SELECT chooses columns, FROM chooses the table, ORDER BY sorts the result.'},
    {type:'example', id:'ex.m1.demo', sql:'SELECT id, name, city\nFROM customers\nORDER BY id\nLIMIT 5;', note:'A small read of the customer table. LIMIT keeps the result manageable.',
      result:{columns:['id','name','city'], rows:[['1','Ada Whitfield','Leeds'],['2','Bruno Sala','Milan'],['3','Chidi Okafor','Lagos'],['4','Dara Novak',null],['5','Emi Tanaka','Osaka']]}},
    {type:'exercise', id:'ex.1.1',
      prompt:'Return the name, category and price of every product in the Electronics category, cheapest first.',
      starter:"SELECT\n    -- name, category, price\nFROM products\n-- filter to Electronics\n-- order by price",
      solution:"SELECT name, category, price\nFROM products\nWHERE category = 'Electronics'\nORDER BY price;",
      hints:['Filter with <span class="kbd-inline">WHERE category = \'Electronics\'</span>.','Add <span class="kbd-inline">ORDER BY price</span> (ascending is the default).'],
      contract:{columns:['name','category','price'], column_order:'exact', row_order:'exact'},
      result:{columns:['name','category','price'], rows:[['Aurora Wireless Earbuds','Electronics','59.00'],['Noise-Cancel Earmuffs','Electronics','64.90'],['Pulse Fitness Tracker','Electronics','74.25'],['Vertex Mechanical Keyboard','Electronics','89.50'],['Nimbus Headphones','Electronics','129.00']]}}
  ]},

{ id:'m2', n:2, title:'Joins without row multiplication', kind:'Core skill', minutes:15,
  summary:'Combine customers, orders, items and products safely, without inflating totals.',
  objectives:['Join parent to child tables correctly','Recognise where duplicates come from','Aggregate at the right grain'],
  blocks:[
    intro('Joining <span class="kbd-inline">orders</span> to <span class="kbd-inline">order_items</span> is a one-to-many join: one order fans out into several line rows. That is correct when you want lines, and wrong when you want one value per order. The skill is knowing which grain the question wants.'),
    intro('If you <span class="kbd-inline">COUNT(*)</span> after that fan-out you will count lines, not orders. Count the distinct key you actually care about, or aggregate before joining.'),
    {type:'syntax', sql:'-- one row per order, with a count of its lines\nSELECT o.id,\n       COUNT(oi.id) AS line_items\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nGROUP BY o.id;', note:'The join fans out; GROUP BY brings it back to one row per order.'},
    {type:'example', id:'ex.m2.demo', sql:"SELECT o.id AS order_id, c.name AS customer\nFROM orders o\nJOIN customers c ON c.id = o.customer_id\nWHERE o.status = 'completed'\nORDER BY o.id\nLIMIT 5;", note:'Joining the customer onto each order, kept at order grain.',
      result:{columns:['order_id','customer'], rows:[['1001','Ada Whitfield'],['1002','Ada Whitfield'],['1004','Chidi Okafor'],['1005','Ada Whitfield'],['1006','Bruno Sala']]}},
    {type:'exercise', id:'ex.2.1',
      prompt:'For each order id, return the customer name and the number of line items, most line items first.',
      starter:"SELECT o.id AS order_id,\n       c.name AS customer_name,\n       -- count lines\nFROM orders o\nJOIN customers c ON c.id = o.customer_id\nJOIN order_items oi ON oi.order_id = o.id\n-- group, order",
      solution:"SELECT o.id AS order_id,\n       c.name AS customer_name,\n       COUNT(oi.id) AS line_items\nFROM orders o\nJOIN customers c ON c.id = o.customer_id\nJOIN order_items oi ON oi.order_id = o.id\nGROUP BY o.id, c.name\nORDER BY line_items DESC;",
      hints:['Use <span class="kbd-inline">COUNT(oi.id)</span>, not <span class="kbd-inline">COUNT(*)</span>.','Group by both order id and customer name, then order by the count descending.'],
      contract:{columns:['order_id','customer_name','line_items'], column_order:'exact', row_order:'exact'},
      result:{columns:['order_id','customer_name','line_items'], rows:[['1007','Dara Novak','4'],['1001','Ada Whitfield','3'],['1012','Chidi Okafor','3'],['1005','Ada Whitfield','2'],['1006','Bruno Sala','2']]}}
  ]},

{ id:'m3', n:3, title:'Revenue reports with conditional aggregation', kind:'Practice', minutes:18,
  summary:'Build metrics that respect status and returns, using FILTER and CASE.',
  objectives:['Aggregate only qualifying rows','Use FILTER for conditional counts and sums','Keep discounts at the right grain'],
  blocks:[
    intro('Real revenue is conditional. Cancelled orders do not count. Returns reduce the total. Writing this as one clean aggregate is the difference between a report people trust and one they quietly correct in a spreadsheet.'),
    intro('Postgres lets you attach a condition directly to an aggregate with <span class="kbd-inline">FILTER (WHERE ...)</span>. It reads better than wrapping everything in <span class="kbd-inline">CASE</span> and it keeps the intent visible.'),
    {type:'syntax', sql:'SELECT DATE_TRUNC(\'month\', o.ordered_at)::date AS month,\n       SUM(oi.quantity * oi.unit_price)\n         FILTER (WHERE o.status = \'completed\') AS revenue\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nGROUP BY 1\nORDER BY 1;', note:'FILTER keeps only completed orders in the sum, without a subquery.'},
    {type:'example', id:'ex.m3.demo', sql:"SELECT DATE_TRUNC('month', o.ordered_at)::date AS month,\n       COUNT(*) FILTER (WHERE o.status = 'completed') AS completed,\n       COUNT(*) FILTER (WHERE o.status = 'cancelled') AS cancelled\nFROM orders o\nGROUP BY 1\nORDER BY 1;", note:'Conditional counts side by side.',
      result:{columns:['month','completed','cancelled'], rows:[['2026-01-01','12','2'],['2026-02-01','14','1'],['2026-03-01','11','3'],['2026-04-01','15','2']]}},
    {type:'exercise', id:'ex.3.1',
      prompt:'Return net revenue per month for completed orders only, oldest month first. Net revenue is quantity times unit price.',
      starter:"SELECT DATE_TRUNC('month', o.ordered_at)::date AS month,\n       -- net revenue\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\n-- filter to completed orders only\n-- group and order by month",
      solution:"SELECT DATE_TRUNC('month', o.ordered_at)::date AS month,\n       SUM(oi.quantity * oi.unit_price) AS net_revenue\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nWHERE o.status = 'completed'\nGROUP BY 1\nORDER BY 1;",
      hints:['Multiply <span class="kbd-inline">quantity</span> by <span class="kbd-inline">unit_price</span> inside <span class="kbd-inline">SUM</span>.','Filter with <span class="kbd-inline">WHERE o.status = \'completed\'</span> before grouping.'],
      contract:{columns:['month','net_revenue'], column_order:'exact', row_order:'exact'},
      result:{columns:['month','net_revenue'], rows:[['2026-01-01','48230.00'],['2026-02-01','52610.00'],['2026-03-01','45090.00'],['2026-04-01','61470.00']]}}
  ]},

{ id:'m4', n:4, title:'CTEs and multi-stage analysis', kind:'Core skill', minutes:18,
  summary:'Break a complex question into named, testable steps with WITH.',
  objectives:['Name intermediate results with a CTE','Test each stage on its own','Chain CTEs into a final answer'],
  blocks:[
    intro('A hard question is usually several easy questions in a row. A CTE (a <span class="kbd-inline">WITH</span> block) gives each step a name so you can run it, read it, and fix it in isolation instead of staring at one giant nested query.'),
    intro('The rule of thumb: if you cannot say in one sentence what a stage produces, it deserves its own CTE. You also get to test stages independently, which is how you find where a wrong answer actually went wrong.'),
    {type:'syntax', sql:'WITH per_customer AS (\n    SELECT customer_id, COUNT(*) AS orders\n    FROM orders\n    GROUP BY customer_id\n)\nSELECT * FROM per_customer\nWHERE orders >= 2;', note:'The CTE is one readable unit; the outer query reasons on top of it.'},
    {type:'example', id:'ex.m4.demo', sql:"WITH completed AS (\n    SELECT id, customer_id\n    FROM orders\n    WHERE status = 'completed'\n)\nSELECT customer_id, COUNT(*) AS completed_orders\nFROM completed\nGROUP BY customer_id\nORDER BY completed_orders DESC\nLIMIT 5;", note:'A single CTE filters once; the outer query aggregates.',
      result:{columns:['customer_id','completed_orders'], rows:[['1','5'],['7','4'],['3','4'],['12','3'],['2','2']]}},
    {type:'exercise', id:'ex.4.1',
      prompt:'Using a CTE, count completed orders per customer, then return only customers with three or more. Highest count first.',
      starter:"WITH completed AS (\n    -- orders with status = 'completed'\n)\n-- count per customer, keep 3 or more",
      solution:"WITH completed AS (\n    SELECT id, customer_id\n    FROM orders\n    WHERE status = 'completed'\n),\nper_customer AS (\n    SELECT customer_id, COUNT(*) AS completed_orders\n    FROM completed\n    GROUP BY customer_id\n)\nSELECT customer_id, completed_orders\nFROM per_customer\nWHERE completed_orders >= 3\nORDER BY completed_orders DESC;",
      hints:['Filter completed orders in the first CTE.','Use a second CTE to GROUP BY customer_id, then filter the aggregate in the final SELECT.'],
      contract:{columns:['customer_id','completed_orders'], column_order:'exact', row_order:'exact'},
      result:{columns:['customer_id','completed_orders'], rows:[['1','5'],['3','4'],['7','4']]}}
  ]},

{ id:'m5', n:5, title:'Window functions and ranking', kind:'Advanced', minutes:20,
  summary:'Compare rows inside a group without collapsing them, and handle ties on purpose.',
  objectives:['Use OVER to compute across rows','Choose RANK, DENSE_RANK or ROW_NUMBER','Aggregate first, then rank'],
  blocks:[
    intro('Aggregation collapses rows: five rows become one total. A <em>window</em> function keeps every row and adds a value computed across a set of related rows. That is what lets you say \u201cthis product\u2019s rank within its category\u201d without losing the product row.', 'Compare rows without collapsing them'),
    intro('The shape is always the same: a function, then <span class="kbd-inline">OVER (...)</span> describing the window. <span class="kbd-inline">PARTITION BY</span> splits rows into groups; <span class="kbd-inline">ORDER BY</span> inside the window controls the sequence the function sees.'),
    {type:'syntax', sql:'SELECT name, category, price,\n       RANK() OVER (\n         PARTITION BY category\n         ORDER BY price DESC\n       ) AS price_rank\nFROM products;', note:'Every product row survives; each gains a rank within its category.'},
    {type:'example', id:'ex.m5.demo', sql:"SELECT customer_id, id AS order_id, ordered_at,\n       COUNT(*) OVER (\n         PARTITION BY customer_id\n         ORDER BY ordered_at, id\n       ) AS orders_so_far\nFROM orders\nWHERE status = 'completed'\nORDER BY customer_id, ordered_at\nLIMIT 6;", note:'A running count per customer. No GROUP BY, and no rows lost.',
      result:{columns:['customer_id','order_id','ordered_at','orders_so_far'], rows:[['1','1001','2026-01-04 10:12','1'],['1','1002','2026-01-19 16:40','2'],['1','1005','2026-02-11 11:55','3'],['2','1006','2026-01-30 08:20','1'],['2','1009','2026-02-18 13:05','2'],['3','1004','2026-02-02 14:20','1']]}},
    intro('Three ranking functions, three meanings. <span class="kbd-inline">ROW_NUMBER()</span> always gives distinct 1,2,3. <span class="kbd-inline">RANK()</span> gives ties the same rank and then skips (1,1,3). <span class="kbd-inline">DENSE_RANK()</span> gives ties the same rank and does not skip (1,1,2). The choice is a business decision, not a style one.', 'Rank inside a group'),
    {type:'exercise', id:'ex.5.1',
      prompt:'Rank products within each category by price, highest first, using RANK so ties share a rank. Return name, category, price, and the rank. Highest-priced first.',
      starter:"SELECT name, category, price,\n       -- rank within category\nFROM products\n-- order within the window, then final order",
      solution:"SELECT name, category, price,\n       RANK() OVER (\n         PARTITION BY category\n         ORDER BY price DESC\n       ) AS price_rank\nFROM products\nORDER BY category, price_rank, name;",
      hints:['Use <span class="kbd-inline">RANK() OVER (PARTITION BY category ORDER BY price DESC)</span>.','Alias it <span class="kbd-inline">price_rank</span> and use it in the outer ORDER BY.'],
      contract:{columns:['name','category','price','price_rank'], column_order:'exact', row_order:'exact'},
      result:{columns:['name','category','price','price_rank'], rows:[['Lumen 27-inch 4K Monitor','Electronics','329.00','1'],['Nimbus Headphones','Electronics','129.00','2'],['Vertex Mechanical Keyboard','Electronics','89.50','3'],['Aurora Wireless Earbuds','Electronics','59.00','4'],['Oak Bookshelf','Home','210.00','1']]}},
    intro('Now the real pattern: aggregate first, then rank the aggregate. Ranking individual line items would rank the wrong thing. And when months have no qualifying rows, decide whether the month should appear at all \u2014 that is the contract, and it is decided by the exercise author, not the engine.', 'Aggregate first, then rank'),
    {type:'exercise', id:'ex.5.2',
      prompt:'For each month, return the two product categories with the highest net revenue from completed orders. Return month, category, net_revenue, and rank. Highest revenue first within each month, months oldest first.',
      starter:"WITH monthly AS (\n    SELECT DATE_TRUNC('month', o.ordered_at)::date AS month,\n           p.category,\n           SUM(oi.quantity * oi.unit_price) AS net_revenue\n    FROM orders o\n    JOIN order_items oi ON oi.order_id = o.id\n    JOIN products p ON p.id = oi.product_id\n    -- completed only\n    -- group by month, category\n)\nSELECT * FROM monthly\n-- rank within month, keep top 2",
      solution:"WITH monthly AS (\n    SELECT DATE_TRUNC('month', o.ordered_at)::date AS month,\n           p.category,\n           SUM(oi.quantity * oi.unit_price) AS net_revenue\n    FROM orders o\n    JOIN order_items oi ON oi.order_id = o.id\n    JOIN products p ON p.id = oi.product_id\n    WHERE o.status = 'completed'\n    GROUP BY 1, 2\n),\nranked AS (\n    SELECT month, category, net_revenue,\n           RANK() OVER (\n             PARTITION BY month\n             ORDER BY net_revenue DESC\n           ) AS rank\n    FROM monthly\n)\nSELECT month, category, net_revenue, rank\nFROM ranked\nWHERE rank <= 2\nORDER BY month, rank, category;",
      hints:['Aggregate to one row per month and category in the CTE first.','Apply <span class="kbd-inline">RANK() OVER (PARTITION BY month ORDER BY net_revenue DESC)</span> to the aggregate, then filter <span class="kbd-inline">rank &lt;= 2</span>.'],
      contract:{columns:['month','category','net_revenue','rank'], column_order:'exact', row_order:'exact'},
      result:{columns:['month','category','net_revenue','rank'], rows:[['2026-01-01','Electronics','48230.00','1'],['2026-01-01','Home','31840.00','2'],['2026-02-01','Home','52610.00','1'],['2026-02-01','Electronics','45090.00','2'],['2026-03-01','Electronics','39120.00','1'],['2026-03-01','Outdoor','27400.00','2']]}}
  ]},

{ id:'m6', n:6, title:'Repeat purchase and cohort retention', kind:'Advanced', minutes:22,
  summary:'Define a cohort, then measure who comes back using window functions.',
  objectives:['Define repeat status at the right grain','Build a cohort key from first purchase','Measure monthly activity per cohort'],
  blocks:[
    intro('Retention questions are grain traps. A customer is only \u201crepeat\u201d as of a given month if they had at least two completed orders by then \u2014 the status changes over time, so it must be computed per customer-month, not once for the whole history.'),
    intro('Cohorts come from a first event: the month of a customer\u2019s first purchase. Everything after is measured relative to that month. A running <span class="kbd-inline">COUNT(*) OVER</span> by order date gives you the \u201crepeat as of\u201d flag cleanly.'),
    {type:'syntax', sql:'-- repeat, as of each order\nCOUNT(*) OVER (\n  PARTITION BY customer_id\n  ORDER BY ordered_at, id\n) AS orders_so_far', note:'orders_so_far >= 2 means this customer is a repeat customer at this point in time.'},
    {type:'example', id:'ex.m6.demo', sql:"WITH first_order AS (\n    SELECT customer_id,\n           DATE_TRUNC('month', MIN(ordered_at))::date AS cohort_month\n    FROM orders\n    WHERE status = 'completed'\n    GROUP BY customer_id\n)\nSELECT f.cohort_month,\n       DATE_TRUNC('month', o.ordered_at)::date AS activity_month,\n       COUNT(DISTINCT o.customer_id) AS active_customers\nFROM orders o\nJOIN first_order f ON f.customer_id = o.customer_id\nWHERE o.status = 'completed'\nGROUP BY 1, 2\nORDER BY 1, 2\nLIMIT 8;", note:'The cohort grid: rows are a customer\u2019s first-purchase month, columns are the months they came back. One customer is counted once per activity month, never twice.',
      result:{columns:['cohort_month','activity_month','active_customers'], rows:[['2026-01-01','2026-01-01','6'],['2026-01-01','2026-02-01','2'],['2026-01-01','2026-03-01','2'],['2026-01-01','2026-04-01','1'],['2026-02-01','2026-02-01','6'],['2026-02-01','2026-03-01','4'],['2026-02-01','2026-04-01','2'],['2026-03-01','2026-03-01','3']]}},
    {type:'exercise', id:'ex.6.1',
      prompt:'Build a monthly report that shows, for completed orders, the number of distinct customers who were already repeat customers (two or more completed orders up to and including that month), month ascending.',
      starter:"WITH history AS (\n    SELECT customer_id,\n           DATE_TRUNC('month', ordered_at)::date AS month,\n           COUNT(*) OVER (\n             PARTITION BY customer_id\n             ORDER BY ordered_at, id\n           ) AS orders_so_far\n    FROM orders\n    -- completed only\n)\n-- count distinct repeat customers per month",
      solution:"WITH history AS (\n    SELECT customer_id,\n           DATE_TRUNC('month', ordered_at)::date AS month,\n           COUNT(*) OVER (\n             PARTITION BY customer_id\n             ORDER BY ordered_at, id\n           ) AS orders_so_far\n    FROM orders\n    WHERE status = 'completed'\n)\nSELECT month,\n       COUNT(DISTINCT customer_id) AS repeat_customers\nFROM history\nWHERE orders_so_far >= 2\nGROUP BY month\nORDER BY month;",
      hints:['Compute <span class="kbd-inline">orders_so_far</span> with a running COUNT window.','Keep rows where <span class="kbd-inline">orders_so_far &gt;= 2</span>, then <span class="kbd-inline">COUNT(DISTINCT customer_id)</span> per month.'],
      contract:{columns:['month','repeat_customers'], column_order:'exact', row_order:'exact'},
      result:{columns:['month','repeat_customers'], rows:[['2026-01-01','0'],['2026-02-01','6'],['2026-03-01','11'],['2026-04-01','14']]}}
  ]},

{ id:'m7', n:7, title:'Read and improve a query plan', kind:'Production', minutes:20,
  summary:'Use EXPLAIN to see what the engine actually does, and why it is slow.',
  objectives:['Read EXPLAIN output top to bottom','Spot a sequential scan that wants an index','Reason about join strategy'],
  blocks:[
    intro('A query plan is the engine\u2019s own account of how it will answer you. You read it inside-out: the deepest node runs first and feeds its parent. Names to know: <span class="kbd-inline">Seq Scan</span> reads the whole table, <span class="kbd-inline">Index Scan</span> jumps via an index, <span class="kbd-inline">Hash Join</span> builds a hash of one side.'),
    intro('The honest caveat: <span class="kbd-inline">EXPLAIN ANALYZE</span> actually runs the statement, so it is only safe on read-only queries with a deadline. Plans depend on statistics, so a plan is evidence about this dataset and this engine version \u2014 not a universal truth about your query.'),
    {type:'syntax', sql:'EXPLAIN\nSELECT * FROM orders\nWHERE customer_id = 1;', note:'Plain EXPLAIN shows the plan without executing the query.'},
    {type:'example', id:'ex.m7.demo', sql:"EXPLAIN ANALYZE\nSELECT p.category,\n       SUM(oi.quantity * oi.unit_price) AS revenue\nFROM order_items oi\nJOIN products p ON p.id = oi.product_id\nGROUP BY p.category\nORDER BY revenue DESC;", note:'Read it inside-out: the deepest node runs first and feeds its parent. Compare estimated <span class="kbd-inline">rows=</span> with actual rows \u2014 a large gap is the first place to look when a plan is wrong.',
      result:{columns:['QUERY PLAN'], rows:[["GroupAggregate  (cost=1.23..3.45 rows=6 width=40) (actual time=0.05..0.21 rows=6 loops=1)"],["  Group Key: p.category"],["  ->  Sort  (cost=1.23..1.24 rows=6 width=32) (actual time=0.09..0.10 rows=160 loops=1)"],["        Sort Key: (sum((oi.quantity * oi.unit_price))) DESC"],["        ->  Hash Join  (cost=1.05..1.15 rows=6 width=32) (actual time=0.04..0.14 rows=160 loops=1)"],["              Hash Cond: (p.id = oi.product_id)"],["              ->  Seq Scan on products p  (cost=0.00..1.08 rows=20 width=8) (actual time=0.01..0.01 rows=20 loops=1)"],["              ->  Hash  (cost=1.02..1.02 rows=2 width=12) (actual time=0.02..0.02 rows=160 loops=1)"],["                    ->  Seq Scan on order_items oi  (cost=0.00..1.02 rows=2 width=12) (actual time=0.01..0.05 rows=160 loops=1)"],["Planning Time: 0.212 ms"],["Execution Time: 0.310 ms"]]}},
    {type:'exercise', id:'ex.7.1',
      prompt:'Write the query to count completed orders per month, then (in a comment) name the access method you would expect on the orders table for a status filter, and why a plain index on status might not help.',
      starter:"SELECT DATE_TRUNC('month', ordered_at)::date AS month,\n       COUNT(*) AS completed\nFROM orders\nWHERE status = 'completed'\nGROUP BY 1\nORDER BY 1;\n-- expected access method:\n-- why an index on status may not help:",
      solution:"SELECT DATE_TRUNC('month', ordered_at)::date AS month,\n       COUNT(*) AS completed\nFROM orders\nWHERE status = 'completed'\nGROUP BY 1\nORDER BY 1;\n-- expected access method: sequential scan (or a partial index on status = 'completed')\n-- why: status has very few distinct values, so a plain B-tree index is not selective;\n--     a partial index or a covering index is the better fit.",
      hints:['Count and group by month first.','A low-cardinality column is a poor index target; mention selectivity.'],
      contract:{columns:['month','completed'], column_order:'exact', row_order:'exact'},
      result:{columns:['month','completed'], rows:[['2026-01-01','12'],['2026-02-01','14'],['2026-03-01','11'],['2026-04-01','15']]}}
  ]},

{ id:'m8', n:8, title:'Capstone: marketplace performance review', kind:'Challenge', minutes:30,
  summary:'Combine joins, CTEs, windows and business reasoning in one open-ended task.',
  objectives:['Decompose an open question','Reuse patterns end to end','Justify each grain decision'],
  blocks:[
    intro('There is no starter scaffold this time. You are given a business question and the whole toolkit. This is the exercise that looks like the job: ambiguous scope, several valid structures, and a result that must be correct at a specific grain.'),
    intro('A good answer here is not the longest query. It is the one where every CTE has a one-sentence purpose and the final grain is exactly what the question asked for.'),
    {type:'example', id:'ex.m8.demo', sql:"WITH monthly AS (\n    SELECT DATE_TRUNC('month', o.ordered_at)::date AS month,\n           SUM(oi.quantity * oi.unit_price) AS net_revenue\n    FROM orders o\n    JOIN order_items oi ON oi.order_id = o.id\n    WHERE o.status = 'completed'\n    GROUP BY 1\n)\nSELECT month,\n       net_revenue,\n       ROUND(100.0 * net_revenue / SUM(net_revenue) OVER (), 1) AS pct_of_period\nFROM monthly\nORDER BY month;", note:'Two capstone moves in one statement: aggregate to the month grain a CTE, then use a window total to turn each month into a share of the whole period without a second pass over the data.',
      result:{columns:['month','net_revenue','pct_of_period'], rows:[['2026-01-01','2132.55','20.8'],['2026-02-01','2311.07','22.6'],['2026-03-01','2456.40','24'],['2026-04-01','1803.62','17.6'],['2026-05-01','1537.95','15']]}},
    {type:'exercise', id:'ex.8.1',
      prompt:'For each month, return the top category by net revenue (completed orders only) and the number of distinct customers who bought in that category that month. Highest revenue first; one row per month, months ascending.',
      starter:"WITH lines AS (\n    SELECT DATE_TRUNC('month', o.ordered_at)::date AS month,\n           p.category,\n           o.customer_id,\n           oi.quantity * oi.unit_price AS revenue\n    FROM orders o\n    JOIN order_items oi ON oi.order_id = o.id\n    JOIN products p ON p.id = oi.product_id\n    -- completed only\n)\n-- aggregate, rank, keep top category",
      solution:"WITH lines AS (\n    SELECT DATE_TRUNC('month', o.ordered_at)::date AS month,\n           p.category,\n           o.customer_id,\n           oi.quantity * oi.unit_price AS revenue\n    FROM orders o\n    JOIN order_items oi ON oi.order_id = o.id\n    JOIN products p ON p.id = oi.product_id\n    WHERE o.status = 'completed'\n),\nby_cat AS (\n    SELECT month, category,\n           SUM(revenue) AS net_revenue,\n           COUNT(DISTINCT customer_id) AS buyers\n    FROM lines\n    GROUP BY month, category\n),\nranked AS (\n    SELECT month, category, net_revenue, buyers,\n           ROW_NUMBER() OVER (PARTITION BY month ORDER BY net_revenue DESC, category) AS rn\n    FROM by_cat\n)\nSELECT month, category, net_revenue, buyers\nFROM ranked\nWHERE rn = 1\nORDER BY month;",
      hints:['Compute line revenue in a CTE, then aggregate to month and category.','Rank with a stable tie-break (<span class="kbd-inline">net_revenue DESC, category</span>) so the top row is deterministic.'],
      contract:{columns:['month','category','net_revenue','buyers'], column_order:'exact', row_order:'exact'},
      result:{columns:['month','category','net_revenue','buyers'], rows:[['2026-01-01','Electronics','48230.00','18'],['2026-02-01','Home','52610.00','21'],['2026-03-01','Electronics','39120.00','16'],['2026-04-01','Home','58040.00','23']]}}
  ]},

{ id:'m9', n:9, title:'Recursive CTEs: build the rows you are missing', kind:'Advanced', minutes:20,
  summary:'Generate a series from one starting row with WITH RECURSIVE, then join it back to fill the gaps.',
  objectives:['Write a recursive CTE with a base case and a recursive term','Keep the recursion terminating with a stop condition','Generate a date or number series and join it back to the data'],
  blocks:[
    intro('Some answers need rows that do not exist yet. A revenue-by-month report should still show a month with no orders \u2014 as a zero, not as a missing line. A recursive CTE builds those rows from a single starting row: you write a base case, then a step that reads the CTE\u2019s own output to produce the next row, and the engine repeats the step until it stops producing rows.'),
    intro('<span class="kbd-inline">WITH RECURSIVE</span> has two parts joined by <span class="kbd-inline">UNION ALL</span>. The base case runs once; the recursive term runs repeatedly, each time reading the rows the previous pass added. Use <span class="kbd-inline">UNION ALL</span>, not <span class="kbd-inline">UNION</span>: the stop condition ends the loop, and you do not want the engine spending time de-duplicating each pass.'),
    {type:'syntax', sql:"WITH RECURSIVE series(month) AS (\n    SELECT DATE '2026-01-01'\n    UNION ALL\n    SELECT (month + INTERVAL '1 month')::date\n    FROM series\n    WHERE month < DATE '2026-05-01'\n)\nSELECT TO_CHAR(month, 'YYYY-MM') AS month\nFROM series\nORDER BY month;", note:'Base case: one date. Recursive term: the next month, kept only while the current one is before May. Five rows come out.'},
    {type:'example', id:'ex.m9.demo', sql:"WITH RECURSIVE series(month) AS (\n    SELECT DATE '2026-01-01'\n    UNION ALL\n    SELECT (month + INTERVAL '1 month')::date\n    FROM series\n    WHERE month < DATE '2026-05-01'\n)\nSELECT TO_CHAR(month, 'YYYY-MM') AS month\nFROM series\nORDER BY month;", note:'The stop is a plain WHERE inside the recursive term: when it filters everything out, the loop is done.',
      result:{columns:['month'], rows:[['2026-01'],['2026-02'],['2026-03'],['2026-04'],['2026-05']]}},
    intro('The payoff is a gap-fill. <span class="kbd-inline">GROUP BY</span> can only report months that exist in the data; a series supplies the missing months so a <span class="kbd-inline">LEFT JOIN</span> can carry them through as 0. The same shape generates numbers, business days, or a tree walk over a table that points at itself \u2014 anything where the next row is a function of the last.'),
    {type:'exercise', id:'ex.m9.1',
      prompt:'Build a series of the six months from January to June 2026 and, for each one, the number of completed orders placed that month (show 0 for a month with none). Return the month as YYYY-MM and the count, oldest first.',
      starter:"WITH RECURSIVE months(month) AS (\n    SELECT DATE '2026-01-01'\n    UNION ALL\n    SELECT (month + INTERVAL '1 month')::date\n    FROM months\n    -- stop after June\n)\nSELECT TO_CHAR(m.month, 'YYYY-MM') AS month,\n       COUNT(o.id) AS completed\nFROM months m\n-- keep months with no orders\n-- aggregate and order",
      solution:"WITH RECURSIVE months(month) AS (\n    SELECT DATE '2026-01-01'\n    UNION ALL\n    SELECT (month + INTERVAL '1 month')::date\n    FROM months\n    WHERE month < DATE '2026-06-01'\n)\nSELECT TO_CHAR(m.month, 'YYYY-MM') AS month,\n       COUNT(o.id) AS completed\nFROM months m\nLEFT JOIN orders o\n       ON DATE_TRUNC('month', o.ordered_at)::date = m.month\n      AND o.status = 'completed'\nGROUP BY m.month\nORDER BY m.month;",
      hints:['The recursive term needs <span class="kbd-inline">WHERE month &lt; DATE \'2026-06-01\'</span> or the loop never stops.','Use a <span class="kbd-inline">LEFT JOIN</span> from the series so a month with no orders still survives.','Put <span class="kbd-inline">o.status = \'completed\'</span> in the <span class="kbd-inline">ON</span> clause, not <span class="kbd-inline">WHERE</span>, or the left join collapses to an inner join.','Count <span class="kbd-inline">o.id</span>, not <span class="kbd-inline">*</span>, so empty months count 0.'],
      contract:{columns:['month','completed'], column_order:'ignored', row_order:'exact'},
      result:{columns:['month','completed'], rows:[['2026-01',8],['2026-02',8],['2026-03',9],['2026-04',7],['2026-05',5],['2026-06',0]]}}
  ]}
];

var JOINS_MODULES: CourseModule[] = [
{ id:'j1', n:1, title:'What a join actually does', kind:'Core skill', minutes:14,
  summary:'Match rows between two tables with ON, and see exactly which rows survive.',
  objectives:['Join a child table to its parent','Understand what ON matches','Keep the result at the grain you asked for'],
  blocks:[
    intro('A join walks two tables and pairs rows where a condition holds. <span class="kbd-inline">orders.customer_id</span> equals <span class="kbd-inline">customers.id</span>, so an inner join keeps only orders whose customer exists — and only customers who have orders.'),
    intro('Read the join as a sentence: “for every order, find the customer it belongs to.” The <span class="kbd-inline">ON</span> clause is the matching rule; everything else you already know.'),
    {type:'syntax', sql:'SELECT o.id AS order_id, c.name AS customer\nFROM orders o\nJOIN customers c ON c.id = o.customer_id\nORDER BY o.id;', note:'JOIN (inner) keeps rows that match on both sides.'},
    {type:'example', id:'ex.j1.demo', sql:"SELECT o.id AS order_id, c.name AS customer\nFROM orders o\nJOIN customers c ON c.id = o.customer_id\nORDER BY o.id\nLIMIT 4;", note:'One row per order, enriched with the customer name.',
      result:{columns:['order_id','customer'], rows:[['1001','Ada Whitfield'],['1002','Ada Whitfield'],['1004','Chidi Okafor'],['1005','Ada Whitfield']]}},
    {type:'exercise', id:'ex.j1.1',
      prompt:'Return each order id, its customer name and its status, newest order first.',
      starter:"SELECT o.id AS order_id,\n       c.name AS customer_name,\n       -- status\nFROM orders o\n-- join customers\n-- newest first",
      solution:"SELECT o.id AS order_id,\n       c.name AS customer_name,\n       o.status\nFROM orders o\nJOIN customers c ON c.id = o.customer_id\nORDER BY o.ordered_at DESC;",
      hints:['Join <span class="kbd-inline">customers c ON c.id = o.customer_id</span>.','Order by <span class="kbd-inline">o.ordered_at DESC</span> for newest first.'],
      contract:{columns:['order_id','customer_name','status'], column_order:'exact', row_order:'ignored'},
      result:{columns:['order_id','customer_name','status'], rows:[['1012','Chidi Okafor','completed'],['1009','Bruno Sala','completed'],['1005','Ada Whitfield','completed'],['1004','Chidi Okafor','completed'],['1001','Ada Whitfield','completed']]}}
  ]},

{ id:'j2', n:2, title:'Left joins and the rows that disappear', kind:'Core skill', minutes:16,
  summary:'Use LEFT JOIN to keep every row on one side — including customers with no orders.',
  objectives:['Keep unmatched rows with LEFT JOIN','Count related rows without losing zeros','Spot an inner join hiding missing data'],
  blocks:[
    intro('An inner <span class="kbd-inline">JOIN</span> is silently a filter: rows with no match vanish. A <span class="kbd-inline">LEFT JOIN</span> keeps every row from the left table and fills the right side with <span class="kbd-inline">NULL</span> when nothing matches.'),
    intro('Because unmatched rows appear with <span class="kbd-inline">NULL</span> on the right, <span class="kbd-inline">COUNT(o.id)</span> counts matches and returns 0, while <span class="kbd-inline">COUNT(*)</span> would wrongly count the customer row itself.'),
    {type:'syntax', sql:'SELECT c.name, COUNT(o.id) AS orders\nFROM customers c\nLEFT JOIN orders o ON o.customer_id = c.id\nGROUP BY c.id, c.name\nORDER BY orders DESC;', note:'LEFT JOIN keeps customers even with zero orders.'},
    {type:'example', id:'ex.j2.demo', sql:"SELECT c.name, COUNT(o.id) AS orders\nFROM customers c\nLEFT JOIN orders o ON o.customer_id = c.id\nGROUP BY c.id, c.name\nORDER BY orders DESC\nLIMIT 4;", note:'Customers with no orders show a count of 0.',
      result:{columns:['name','orders'], rows:[['Ada Whitfield','5'],['Chidi Okafor','4'],['Bruno Sala','2'],['Emi Tanaka','0']]}},
    {type:'exercise', id:'ex.j2.1',
      prompt:'Return every customer name and how many orders they placed, including customers with none. Highest count first.',
      starter:"SELECT c.name, COUNT(o.id) AS orders\nFROM customers c\n-- left join orders\n-- group by customer\n-- highest first",
      solution:"SELECT c.name, COUNT(o.id) AS orders\nFROM customers c\nLEFT JOIN orders o ON o.customer_id = c.id\nGROUP BY c.id, c.name\nORDER BY orders DESC, c.name;",
      hints:['Use <span class="kbd-inline">LEFT JOIN</span>, not JOIN.','Count <span class="kbd-inline">o.id</span> so unmatched rows count as 0.'],
      contract:{columns:['name','orders'], column_order:'exact', row_order:'ignored'},
      result:{columns:['name','orders'], rows:[['Ada Whitfield','5'],['Chidi Okafor','4'],['Bruno Sala','2'],['Dara Novak','0']]}}
  ]},

{ id:'j3', n:3, title:'The grain trap: one-to-many fan-out', kind:'Advanced', minutes:18,
  summary:'Joining a parent to its children multiplies rows — count the right thing.',
  objectives:['See why joins fan rows out','Count distinct keys after a fan-out','Aggregate at the grain the question wants'],
  blocks:[
    intro('One order has many line items. Join them and each order row multiplies into one row per line. That is correct when you want lines and wrong when you want orders — and <span class="kbd-inline">COUNT(*)</span> after the join counts lines, not orders.'),
    intro('The fix is to name the grain: <span class="kbd-inline">COUNT(DISTINCT o.id)</span> counts orders even after the fan-out. When two numbers disagree, the grain is usually why.'),
    {type:'syntax', sql:'SELECT o.id, COUNT(oi.id) AS lines\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nGROUP BY o.id;', note:'GROUP BY brings the fan-out back to one row per order.'},
    {type:'example', id:'ex.j3.demo', sql:"SELECT o.id AS order_id, COUNT(oi.id) AS lines\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nGROUP BY o.id\nORDER BY lines DESC\nLIMIT 3;", note:'How many lines each order contains.',
      result:{columns:['order_id','lines'], rows:[['1007','4'],['1001','3'],['1012','3']]}},
    {type:'exercise', id:'ex.j3.1',
      prompt:'Return each product name and how many distinct orders it appears in, most first. Count orders, not line items.',
      starter:"SELECT p.name, COUNT(DISTINCT oi.order_id) AS orders\nFROM products p\n-- join order_items\n-- group by product\n-- most first",
      solution:"SELECT p.name, COUNT(DISTINCT oi.order_id) AS orders\nFROM products p\nJOIN order_items oi ON oi.product_id = p.id\nGROUP BY p.id, p.name\nORDER BY orders DESC, p.name;",
      hints:['Join <span class="kbd-inline">order_items oi ON oi.product_id = p.id</span>.','Use <span class="kbd-inline">COUNT(DISTINCT oi.order_id)</span> to avoid counting lines twice.'],
      contract:{columns:['name','orders'], column_order:'exact', row_order:'ignored'},
      result:{columns:['name','orders'], rows:[['Aurora Wireless Earbuds','18'],['Nimbus Headphones','14'],['Linen Duvet Cover','11']]}}
  ]}
];

/* ---------- course platform model ---------- */
var SUBQUERIES_MODULES: CourseModule[] = [
{ id:'q1', n:1, title:'Subqueries in WHERE', kind:'Core skill', minutes:15,
  summary:'Use one query as the input to another without leaving the outer query behind.',
  objectives:['Read a scalar subquery as a single value','Filter with a list subquery','Keep the outer query at its own grain'],
  blocks:[
    intro('A subquery is an ordinary SELECT used as a value, a list, or a table inside a larger statement. The outer query never sees the subquery\u2019s rows directly \u2014 it sees whatever the subquery collapses to. That collapse is the whole idea, and it decides which shape you are allowed to use.'),
    intro('Three shapes cover almost everything. <span class="kbd-inline">(SELECT ...)</span> that returns one row and one column is a scalar value you compare with <span class="kbd-inline">=</span>. One column with many rows is a list you test with <span class="kbd-inline">IN</span>. Many columns is a table you join against in <span class="kbd-inline">FROM</span>.'),
    {type:'syntax', sql:'SELECT name, price\nFROM products\nWHERE price > (SELECT AVG(price) FROM products)\nORDER BY price DESC;', note:'The subquery collapses to one number, so the outer WHERE compares a column to a value.'},
    {type:'example', id:'ex.q1.demo', sql:"SELECT name\nFROM customers\nWHERE id IN (\n    SELECT customer_id FROM orders WHERE status = 'completed'\n)\nORDER BY name\nLIMIT 4;", note:'The list subquery supplies the ids; the outer query still returns one row per customer.',
      result:{columns:['name'], rows:[['Ada Whitfield'],['Bruno Sala'],['Chidi Okafor'],['Dara Novak']]}},
    intro('The mistake to avoid is running the subquery in your head once rather than for the rows it is compared against. A scalar subquery is re-evaluated as a value in the row being tested, which is exactly the difference between the uncorrelated subqueries here and the correlated ones in the next chapter.'),
    {type:'exercise', id:'ex.q1.1',
      prompt:'Return the name and price of every product priced above the average product price. Most expensive first, then by name.',
      starter:"SELECT name, price\nFROM products\n-- filter above the average price\n-- most expensive first",
      solution:"SELECT name, price\nFROM products\nWHERE price > (SELECT AVG(price) FROM products)\nORDER BY price DESC, name;",
      hints:['The average comes from <span class="kbd-inline">(SELECT AVG(price) FROM products)</span>.','Compare the column against that scalar, not against a hard-coded number.'],
      contract:{columns:['name','price'], column_order:'exact', row_order:'exact'},
      result:{columns:['name','price'], rows:[['Lumen 27-inch 4K Monitor','329.00'],['Oak Bookshelf','210.00'],['Trail Runner Jacket','139.00'],['Nimbus Headphones','129.00']]}}
  ]},

{ id:'q2', n:2, title:'Correlated subqueries and EXISTS', kind:'Core skill', minutes:16,
  summary:'Let the inner query read the outer row, and use EXISTS when you only care whether a match exists.',
  objectives:['Write a subquery that references the outer row','Use EXISTS and NOT EXISTS','Know when a correlated query is the clearest answer'],
  blocks:[
    intro('A correlated subquery mentions a column from the outer query, so it cannot be evaluated once \u2014 conceptually it runs per outer row. That sounds expensive, and it can be, but it is also the clearest way to express \u201cper row, does this exist?\u201d'),
    intro('<span class="kbd-inline">EXISTS</span> is the natural partner. It takes a subquery and returns true as soon as the inner query produces a single row. The inner SELECT list is irrelevant, so the convention is <span class="kbd-inline">SELECT 1</span>; what matters is the <span class="kbd-inline">WHERE</span> clause that ties the inner table back to the outer one.'),
    {type:'syntax', sql:'SELECT c.name\nFROM customers c\nWHERE NOT EXISTS (\n    SELECT 1 FROM orders o\n    WHERE o.customer_id = c.id\n)\nORDER BY c.name;', note:'EXISTS stops at the first matching row, so it is a test, not a fetch.'},
    {type:'example', id:'ex.q2.demo', sql:"SELECT p.name,\n       (SELECT COUNT(*) FROM order_items oi WHERE oi.product_id = p.id) AS lines\nFROM products p\nORDER BY p.id\nLIMIT 4;", note:'A correlated scalar subquery computes one count per product row.',
      result:{columns:['name','lines'], rows:[['Aurora Wireless Earbuds','24'],['Nimbus Headphones','18'],['Linen Duvet Cover','15'],['Cedar Desk Lamp','11']]}},
    intro('Correlated queries and joins are often interchangeable, and the deciding factor is the grain. If the inner query can return more than one row, a correlated scalar subquery fails at run time while a join multiplies rows; the subquery keeps the outer grain honest.'),
    {type:'exercise', id:'ex.q2.1',
      prompt:'For every product, return its name and the number of order lines that reference it, including products never ordered, which show 0. Lowest product id first.',
      starter:"SELECT p.name,\n       -- number of lines\nFROM products p\n-- keep one row per product\nORDER BY p.id;",
      solution:"SELECT p.name,\n       (SELECT COUNT(*) FROM order_items oi WHERE oi.product_id = p.id) AS lines\nFROM products p\nORDER BY p.id;",
      hints:['Correlate the inner query with <span class="kbd-inline">oi.product_id = p.id</span>.','COUNT of an empty set is 0, so no LEFT JOIN and no COALESCE are needed.'],
      contract:{columns:['name','lines'], column_order:'exact', row_order:'exact'},
      result:{columns:['name','lines'], rows:[['Aurora Wireless Earbuds','24'],['Nimbus Headphones','18'],['Linen Duvet Cover','15'],['Cedar Desk Lamp','11'],['Trail Runner Jacket','9']]}}
  ]},

{ id:'q3', n:3, title:'IN, EXISTS and the NULL trap', kind:'Practice', minutes:16,
  summary:'Two ways to ask the same question, one of which silently returns nothing when NULLs appear.',
  objectives:['Explain why NOT IN can return no rows','Choose the NULL-safe form','Read a set comparison as three-valued logic'],
  blocks:[
    intro('<span class="kbd-inline">IN</span> and <span class="kbd-inline">EXISTS</span> answer the same question for most queries, so the choice looks like taste. It is not, once <span class="kbd-inline">NULL</span> is involved.'),
    intro('The rule behind the trap is three-valued logic. <span class="kbd-inline">x NOT IN (a, b, NULL)</span> expands to <span class="kbd-inline">x &lt;&gt; a AND x &lt;&gt; b AND x &lt;&gt; NULL</span>, and the last comparison is neither true nor false \u2014 it is unknown. One unknown term makes the whole conjunction not-true, so the row is dropped. A single NULL anywhere in the list empties the result.'),
    {type:'syntax', sql:'-- Not safe: one NULL in the list kills every row\nSELECT name FROM customers\nWHERE id NOT IN (SELECT customer_id FROM orders);\n\n-- Safe: EXISTS only asks whether a matching row exists\nSELECT name FROM customers c\nWHERE NOT EXISTS (\n    SELECT 1 FROM orders o WHERE o.customer_id = c.id\n);', note:'When the outer column can be NULL the same reasoning applies to the left side too.'},
    {type:'example', id:'ex.q3.demo', sql:"SELECT c.name\nFROM customers c\nWHERE NOT EXISTS (\n    SELECT 1 FROM orders o WHERE o.customer_id = c.id\n)\nORDER BY c.name\nLIMIT 4;", note:'NOT EXISTS returns the unmatched customers regardless of NULLs.',
      result:{columns:['name'], rows:[['Emi Tanaka'],['Farah Haddad'],['Greta Lindqvist'],['Hana Sato']]}},
    intro('The practical rule: use <span class="kbd-inline">NOT EXISTS</span> (or a LEFT JOIN with <span class="kbd-inline">IS NULL</span>) whenever the subquery column can be NULL. Use <span class="kbd-inline">NOT IN</span> only when you can see, from the schema, that the column is <span class="kbd-inline">NOT NULL</span>.'),
    {type:'exercise', id:'ex.q3.1',
      prompt:'Return the name of every customer who has never placed an order, ordered by name. Use the NULL-safe form.',
      starter:"SELECT c.name\nFROM customers c\n-- customers with no orders\nORDER BY c.name;",
      solution:"SELECT c.name\nFROM customers c\nWHERE NOT EXISTS (\n    SELECT 1 FROM orders o WHERE o.customer_id = c.id\n)\nORDER BY c.name;",
      hints:['<span class="kbd-inline">NOT EXISTS</span> is safe here; <span class="kbd-inline">NOT IN</span> is not.','Tie the inner query to the outer row with <span class="kbd-inline">o.customer_id = c.id</span>.'],
      contract:{columns:['name'], column_order:'exact', row_order:'exact'},
      result:{columns:['name'], rows:[['Emi Tanaka'],['Farah Haddad'],['Greta Lindqvist'],['Hana Sato']]}}
  ]},

{ id:'q4', n:4, title:'Set operations: UNION, INTERSECT, EXCEPT', kind:'Advanced', minutes:18,
  summary:'Stack and compare whole result sets, and know when a join is the wrong tool.',
  objectives:['Stack results with UNION and UNION ALL','Compare sets with INTERSECT and EXCEPT','Match column counts and types'],
  blocks:[
    intro('Joins combine columns from two tables side by side. Set operations combine whole result sets on top of one another. When the question is \u201cwhich of these appear in both lists?\u201d a set operation says it directly.'),
    intro('All four operators require the same number of columns, in the same order, with compatible types. The difference is the semantics: <span class="kbd-inline">UNION</span> removes duplicate rows, <span class="kbd-inline">UNION ALL</span> keeps every row, <span class="kbd-inline">INTERSECT</span> keeps rows present in both inputs, and <span class="kbd-inline">EXCEPT</span> keeps rows from the first input that are absent from the second.'),
    {type:'syntax', sql:"SELECT name FROM products WHERE category = 'Home'\nEXCEPT\nSELECT p.name\nFROM products p\nJOIN order_items oi ON oi.product_id = p.id;", note:'Home products minus the ones that have ever sold.'},
    {type:'example', id:'ex.q4.demo', sql:"SELECT category FROM products WHERE active\nEXCEPT\nSELECT category FROM products WHERE NOT active\nORDER BY category;", note:'Categories that only ever hold active products.',
      result:{columns:['category'], rows:[['Electronics'],['Home'],['Kitchen']]}},
    intro('Two habits make set operations predictable. Alias only the first SELECT, because the aliases come from it. And reach for <span class="kbd-inline">UNION ALL</span> by default when the inputs cannot overlap \u2014 it tells the reader you have thought about duplicates instead of leaving a de-duplication to chance.'),
    {type:'exercise', id:'ex.q4.1',
      prompt:'Using a set operation, return the name of every customer who has never placed an order. One column named name, alphabetical.',
      starter:"SELECT name FROM customers\n-- subtract the customers who have ordered\nORDER BY name;",
      solution:"SELECT name FROM customers\nEXCEPT\nSELECT c.name\nFROM customers c\nJOIN orders o ON o.customer_id = c.id\nORDER BY name;",
      hints:['<span class="kbd-inline">EXCEPT</span> subtracts the second result from the first: everyone, minus the names that appear in orders.','Both sides must return one text column, in the same position.','An <span class="kbd-inline">INNER JOIN</span> is enough on the second side \u2014 you only need the customers who do have an order.'],
      contract:{columns:['name'], column_order:'exact', row_order:'exact'},
      result:{columns:['name'], rows:[['Aya Rahman'],['Bo Ivanov'],['Cleo Haidar'],['Dara Novak'],['Dev Larsen'],['Yara Park'],['Zane Lindqvist']]}}
  ]}
];

var FOUNDATIONS_MODULES: CourseModule[] = [
{ id:'f1', n:1, title:'Projection, aliases and DISTINCT', kind:'Foundations', minutes:12,
  summary:'Return exactly the columns the question needs, name them for the reader, and collapse rows that repeat with DISTINCT.',
  objectives:['Select only the columns the question needs','Rename output columns with AS','Remove duplicate rows with DISTINCT, which applies to the whole row'],
  blocks:[
    intro('A <span class="kbd-inline">SELECT</span> returns a table, and the column list decides what is in it. <span class="kbd-inline">SELECT *</span> hands back whatever columns the table happens to own today; naming the columns you need keeps a result readable and keeps a report from changing shape the next time someone adds a column.'),
    intro('Rename a column with <span class="kbd-inline">AS</span> when the stored name is not the name your reader wants. <span class="kbd-inline">AS</span> changes only the output label \u2014 the table is untouched \u2014 so it is the right place to expand an abbreviation or to name a value the query computed.'),
    {type:'syntax', sql:"SELECT DISTINCT category\nFROM products\nORDER BY category;", note:'DISTINCT removes duplicate rows from the result; ORDER BY makes the output stable.'},
    {type:'example', id:'ex.f1.demo', sql:"SELECT DISTINCT category\nFROM products\nORDER BY category;", note:'Twenty products, four distinct categories: DISTINCT collapses the repeats.',
      result:{columns:['category'], rows:[['Electronics'],['Home'],['Kitchen'],['Outdoor']]}},
    intro('The subtlety is that <span class="kbd-inline">DISTINCT</span> applies to the whole row, not to one column. Ask for two columns and you get every distinct combination, so <span class="kbd-inline">category, active</span> keeps a separate row for Outdoor whether or not the product is still available \u2014 read it as a set of pairs, not as a de-duplicated list.'),
    {type:'exercise', id:'ex.f1.1',
      prompt:'Return one row per distinct combination of product category and active flag, ordered by category and then by active. Name the columns category and active.',
      starter:"SELECT DISTINCT\n       -- category and active\nFROM products\n-- order by category, then active",
      solution:"SELECT DISTINCT category, active\nFROM products\nORDER BY category, active;",
      hints:['List both columns after <span class="kbd-inline">DISTINCT</span>; it de-duplicates the pair, not each column on its own.','Order by <span class="kbd-inline">category, active</span> to make the output deterministic.'],
      contract:{columns:['category','active'], column_order:'ignored', row_order:'exact'},
      result:{columns:['category','active'], rows:[['Electronics',true],['Home',true],['Kitchen',true],['Outdoor',false],['Outdoor',true]]}}
  ]},

{ id:'f2', n:2, title:'WHERE, AND, OR and NOT', kind:'Foundations', minutes:14,
  summary:'Keep the rows a predicate accepts, and read mixed AND/OR in the order the parser does \u2014 not the order you wrote them.',
  objectives:['Filter rows with WHERE and comparison operators','Combine conditions with AND, OR and NOT','Parenthesise mixed AND/OR so the intent is unambiguous'],
  blocks:[
    intro('<span class="kbd-inline">WHERE</span> keeps the rows for which its predicate is true and drops the rest. It runs before <span class="kbd-inline">SELECT</span> names anything, so it can only see the input columns, which is why <span class="kbd-inline">WHERE price &gt; 100</span> works on a table and an output alias does not.'),
    intro('Conditions combine with <span class="kbd-inline">AND</span>, <span class="kbd-inline">OR</span> and <span class="kbd-inline">NOT</span> under strict precedence: <span class="kbd-inline">NOT</span> binds tightest, then <span class="kbd-inline">AND</span>, then <span class="kbd-inline">OR</span>. <span class="kbd-inline">a OR b AND c</span> is therefore <span class="kbd-inline">a OR (b AND c)</span>, which is almost never what the sentence around it meant.'),
    {type:'syntax', sql:"SELECT name, category, price\nFROM products\nWHERE category = 'Outdoor'\n   OR category = 'Electronics' AND price > 100\nORDER BY price DESC, name;", note:'AND binds before OR, so this is Outdoor OR (Electronics over 100) \u2014 every Outdoor product, plus expensive Electronics.'},
    {type:'example', id:'ex.f2.demo', sql:"SELECT name, category, price\nFROM products\nWHERE category = 'Outdoor'\n   OR category = 'Electronics' AND price > 100\nORDER BY price DESC, name;", note:'The cheap Outdoor rows are the tell: had the OR grouped first, only the two products over 100 would survive.',
      result:{columns:['name','category','price'], rows:[['Trail Runner Jacket','Outdoor',139],['Nimbus Headphones','Electronics',129],['Harbor Rain Shell','Outdoor',118],['Summit Hiking Poles','Outdoor',96.4],['Solar Camp Lantern','Outdoor',47],['Rope Dog Leash','Outdoor',15.5],['Discontinued Camp Stove','Outdoor',0]]}},
    intro('When a predicate mixes <span class="kbd-inline">OR</span> with <span class="kbd-inline">AND</span>, add parentheses even when precedence already gives the answer you want. The parentheses cost nothing and remove the question for the next reader. <span class="kbd-inline">NOT (category = \'Home\')</span> and <span class="kbd-inline">category &lt;&gt; \'Home\'</span> are the same thing; pick one and stay consistent.'),
    {type:'exercise', id:'ex.f2.1',
      prompt:'Return the name, category and price of every active product that is not in the Electronics category and costs at least 90, cheapest first; break ties by name.',
      starter:"SELECT name, category, price\nFROM products\nWHERE -- active, not Electronics, price >= 90\nORDER BY -- price, then name",
      solution:"SELECT name, category, price\nFROM products\nWHERE active = true\n  AND NOT category = 'Electronics'\n  AND price >= 90\nORDER BY price, name;",
      hints:['Keep only active rows: <span class="kbd-inline">active = true</span>.','Exclude a category with <span class="kbd-inline">NOT category = \'Electronics\'</span> (or the equivalent <span class="kbd-inline">category &lt;&gt; \'Electronics\'</span>).','<span class="kbd-inline">price &gt;= 90</span> keeps 90 exactly; sort with <span class="kbd-inline">ORDER BY price, name</span>.'],
      contract:{columns:['name','category','price'], column_order:'ignored', row_order:'exact'},
      result:{columns:['name','category','price'], rows:[['Summit Hiking Poles','Outdoor',96.4],['Harbor Rain Shell','Outdoor',118],['Trail Runner Jacket','Outdoor',139],['Granite Cookware Set','Kitchen',210]]}}
  ]},

{ id:'f3', n:3, title:'ORDER BY, LIMIT and OFFSET', kind:'Foundations', minutes:12,
  summary:'Sort a result so it is reproducible, then page through it with LIMIT and OFFSET \u2014 and a tie-break that keeps the pages honest.',
  objectives:['Sort with ORDER BY and more than one key','Page a result with LIMIT and OFFSET','Add a tie-break so paging cannot repeat or skip a row'],
  blocks:[
    intro('A table has no inherent order; the order you see is whatever the engine found convenient. <span class="kbd-inline">ORDER BY</span> is the only thing that makes a result reproducible, and reproducibility is what lets you say the answer is the same today as yesterday.'),
    intro('Paging uses two clauses together: <span class="kbd-inline">LIMIT n</span> takes the next n rows, and <span class="kbd-inline">OFFSET k</span> skips the first k. Page one is <span class="kbd-inline">LIMIT 3 OFFSET 0</span>, page two is <span class="kbd-inline">LIMIT 3 OFFSET 3</span>, page three is <span class="kbd-inline">LIMIT 3 OFFSET 6</span>. If the sort keys can tie and there is no tie-break, equal rows have no defined order and a row can appear on two pages or none.'),
    {type:'syntax', sql:"SELECT name, price\nFROM products\nORDER BY price DESC, name\nLIMIT 3 OFFSET 3;", note:'Page two of three: skip the first three prices, take the next three. The name tie-break is what makes paging stable.'},
    {type:'example', id:'ex.f3.demo', sql:"SELECT name, price\nFROM products\nORDER BY price DESC, name\nLIMIT 3 OFFSET 3;", note:'Prices 118, 96.40 and 89.50: the three products ranked fourth to sixth once name breaks the ties.',
      result:{columns:['name','price'], rows:[['Harbor Rain Shell',118],['Summit Hiking Poles',96.4],['Vertex Mechanical Keyboard',89.5]]}},
    intro('Deep offsets are a known cost: the engine still sorts and discards the skipped rows, so page 500 reads a lot of data to throw it away. Mature APIs page by a key instead (<span class="kbd-inline">WHERE price &lt; :last_price</span>), but <span class="kbd-inline">LIMIT</span>/<span class="kbd-inline">OFFSET</span> is the shape you will read most, so it is the one to know first.'),
    {type:'exercise', id:'ex.f3.1',
      prompt:'Products are paged three at a time, sorted by price descending with ties broken by name. Return the third page \u2014 the seventh, eighth and ninth rows \u2014 as name and price in display order.',
      starter:"SELECT name, price\nFROM products\nORDER BY -- price descending, then name\nLIMIT -- three rows\nOFFSET -- skip two pages",
      solution:"SELECT name, price\nFROM products\nORDER BY price DESC, name\nLIMIT 3 OFFSET 6;",
      hints:['Page size is 3, so page three skips two pages of three: <span class="kbd-inline">OFFSET 6</span>.','Sort with <span class="kbd-inline">ORDER BY price DESC, name</span> so ties cannot move between pages.'],
      contract:{columns:['name','price'], column_order:'ignored', row_order:'exact'},
      result:{columns:['name','price'], rows:[['Linen Duvet Cover',84],['Pulse Fitness Tracker',74.25],['Bamboo Shelf',72.3]]}}
  ]},

{ id:'f4', n:4, title:'NULL is unknown', kind:'Foundations', minutes:16,
  summary:'Read NULL as "not known", not as zero or empty: compare with IS NULL, reason with three-valued logic, and replace missing values with COALESCE.',
  objectives:['Treat NULL as unknown, never equal to anything','Test missing values with IS NULL and IS DISTINCT FROM','Replace NULL with COALESCE and order it with NULLS FIRST/LAST'],
  blocks:[
    intro('NULL is the absence of a value. It is not zero and it is not an empty string: a customer with a NULL city is a customer whose city we do not know, and treating that as a blank string would be a different claim. Comparisons against NULL are unknown rather than true or false, so <span class="kbd-inline">city = NULL</span> is never true \u2014 even when the city is in fact NULL.'),
    intro('That is three-valued logic: a predicate evaluates to true, false or unknown, and <span class="kbd-inline">WHERE</span> keeps only the true rows. A row whose city is NULL is neither <span class="kbd-inline">= \'Leeds\'</span> nor <span class="kbd-inline">&lt;&gt; \'Leeds\'</span>; both comparisons are unknown, so both filters drop it.'),
    {type:'syntax', sql:"SELECT COUNT(*) FILTER (WHERE city <> 'Leeds')            AS not_leeds,\n       COUNT(*) FILTER (WHERE city IS DISTINCT FROM 'Leeds') AS truly_not_leeds\nFROM customers;", note:'The plain comparison silently loses the four customers whose city is unknown; IS DISTINCT FROM counts them because it treats NULL as a value.'},
    {type:'example', id:'ex.f4.demo', sql:"SELECT COUNT(*) FILTER (WHERE city <> 'Leeds')            AS not_leeds,\n       COUNT(*) FILTER (WHERE city IS DISTINCT FROM 'Leeds') AS truly_not_leeds\nFROM customers;", note:'23 versus 27: the gap is exactly the unknown-city rows a \'not Leeds\' report would have dropped.',
      result:{columns:['not_leeds','truly_not_leeds'], rows:[[23,27]]}},
    intro('To replace a missing value use <span class="kbd-inline">COALESCE(expr, fallback)</span>, which returns the first argument that is not NULL. To sort a column that can be NULL, say where the unknowns go: <span class="kbd-inline">ORDER BY city NULLS LAST</span> puts known cities first and the unknowns at the end (Postgres puts NULLs last for ascending sorts anyway, first for descending, so write it out when the order matters).'),
    {type:'example', id:'ex.f4.demo2', sql:"SELECT name, city\nFROM customers\nWHERE city = 'Milan' OR city IS NULL\nORDER BY city NULLS LAST, name;", note:'Milan first, then the unknown cities: NULLS LAST makes the placement explicit instead of relying on the default.',
      result:{columns:['name','city'], rows:[['Bruno Sala','Milan'],['Noor Berg','Milan'],['Zane Lindqvist','Milan'],['Bo Ivanov',null],['Dara Novak',null],['Luca Novak',null],['Tess Larsen',null]]}},
    {type:'exercise', id:'ex.f4.1',
      prompt:'Return the name and city of every customer whose city is unknown, replacing the missing city with the text \'Unknown\'. Order by name.',
      starter:"SELECT name,\n       -- replace a missing city\nFROM customers\n-- keep the unknown cities\nORDER BY name;",
      solution:"SELECT name, COALESCE(city, 'Unknown') AS city\nFROM customers\nWHERE city IS NULL\nORDER BY name;",
      hints:['The filter is <span class="kbd-inline">WHERE city IS NULL</span>; never <span class="kbd-inline">= NULL</span>.','<span class="kbd-inline">COALESCE(city, \'Unknown\')</span> supplies the label for the missing value.'],
      contract:{columns:['name','city'], column_order:'ignored', row_order:'exact'},
      result:{columns:['name','city'], rows:[['Bo Ivanov','Unknown'],['Dara Novak','Unknown'],['Luca Novak','Unknown'],['Tess Larsen','Unknown']]}}
  ]},

{ id:'f5', n:5, title:'IN, BETWEEN and LIKE', kind:'Foundations', minutes:14,
  summary:'Match against a list, an inclusive range, or a text pattern \u2014 three shorthands for predicates you could spell out with OR.',
  objectives:['Match a set of values with IN','Match an inclusive range with BETWEEN','Match text patterns with LIKE and the % and _ wildcards'],
  blocks:[
    intro('<span class="kbd-inline">IN</span>, <span class="kbd-inline">BETWEEN</span> and <span class="kbd-inline">LIKE</span> are shorthands. Each one is equivalent to a chain of comparisons you could write by hand, but each says something more specific to the reader: membership in a list, membership in a range, or a textual pattern.'),
    intro('<span class="kbd-inline">BETWEEN a AND b</span> is inclusive at both ends, so <span class="kbd-inline">BETWEEN 20 AND 90</span> keeps 20 and 90. <span class="kbd-inline">LIKE</span> matches text with two wildcards: <span class="kbd-inline">%</span> stands for any run of characters (including none) and <span class="kbd-inline">_</span> stands for exactly one. The patterns are case-sensitive; <span class="kbd-inline">ILIKE</span> is the case-insensitive sibling.'),
    {type:'syntax', sql:"SELECT name, category\nFROM products\nWHERE name LIKE 'C%'\n   OR name LIKE 'S%'\nORDER BY name;", note:'A prefix search: names that begin with C or S, with % standing for the rest of the name.'},
    {type:'example', id:'ex.f5.demo', sql:"SELECT name, category\nFROM products\nWHERE name LIKE 'C%'\n   OR name LIKE 'S%'\nORDER BY name;", note:'Five names across three categories \u2014 LIKE is not anchored to a column, it is anchored to the pattern.',
      result:{columns:['name','category'], rows:[['Cedar Desk Lamp','Home'],['Ceramic Vase','Home'],['Copper Kettle','Kitchen'],['Solar Camp Lantern','Outdoor'],['Summit Hiking Poles','Outdoor']]}},
    intro('Two habits avoid the usual traps. Reach for <span class="kbd-inline">IN</span> instead of a long <span class="kbd-inline">OR</span> chain \u2014 <span class="kbd-inline">category IN (\'Home\', \'Kitchen\')</span> reads like the sentence you started from. And remember what <span class="kbd-inline">IN</span> does with NULL: <span class="kbd-inline">x IN (1, NULL)</span> is true for matching values but unknown for everything else, which is the same three-valued logic that rules out <span class="kbd-inline">WHERE city = NULL</span>.'),
    {type:'exercise', id:'ex.f5.1',
      prompt:'Return the name and price of every product in the Home or Kitchen category priced between 20 and 90 inclusive, cheapest first; break ties by name.',
      starter:"SELECT name, price\nFROM products\nWHERE -- category IN (...), price BETWEEN ...\nORDER BY -- price, then name",
      solution:"SELECT name, price\nFROM products\nWHERE category IN ('Home', 'Kitchen')\n  AND price BETWEEN 20 AND 90\nORDER BY price, name;",
      hints:['<span class="kbd-inline">category IN (\'Home\', \'Kitchen\')</span> replaces two <span class="kbd-inline">OR</span>s.','<span class="kbd-inline">price BETWEEN 20 AND 90</span> includes both endpoints.'],
      contract:{columns:['name','price'], column_order:'ignored', row_order:'exact'},
      result:{columns:['name','price'], rows:[['Maple Cutting Board',24],['Ceramic Vase',33.2],['Cedar Desk Lamp',42.5],['Wool Throw Blanket',55],['Copper Kettle',65.75],['Bamboo Shelf',72.3],['Linen Duvet Cover',84]]}}
  ]},

{ id:'f6', n:6, title:'CASE expressions', kind:'Foundations', minutes:14,
  summary:'Turn a value into a label with CASE: test conditions in order, catch everything else with ELSE, and name the result.',
  objectives:['Label values with a searched CASE','Rely on the first matching WHEN and a final ELSE','Reuse the label in ORDER BY or GROUP BY'],
  blocks:[
    intro('<span class="kbd-inline">CASE</span> is an expression, not a statement: it produces a value, so it can stand in any position a column can. That is what makes a searched CASE the tool for banding a number into a label \u2014 <span class="kbd-inline">CASE WHEN price &gt;= 100 THEN \'premium\' ... END AS band</span> adds a column just like any other.'),
    intro('Conditions are tested top to bottom and the first match wins, so order the branches from most specific to least. Without a matching <span class="kbd-inline">WHEN</span> and no <span class="kbd-inline">ELSE</span>, the expression is NULL \u2014 an explicit <span class="kbd-inline">ELSE</span> is almost always what you want, even if it is just <span class="kbd-inline">\'other\'</span>.'),
    {type:'syntax', sql:"SELECT name, price,\n       CASE WHEN price >= 100 THEN 'premium'\n            WHEN price >= 50  THEN 'mid'\n            ELSE 'value' END AS band\nFROM products\nORDER BY price DESC, name;", note:'Two thresholds and a catch-all. The order of WHEN matters: >= 100 must be tested before >= 50.'},
    {type:'example', id:'ex.f6.demo', sql:"SELECT name, price,\n       CASE WHEN price >= 100 THEN 'premium'\n            WHEN price >= 50  THEN 'mid'\n            ELSE 'value' END AS band\nFROM products\nORDER BY price DESC, name\nLIMIT 5;", note:'The band column is ordinary output: sort by it, group by it, or filter it like any other value.',
      result:{columns:['name','price','band'], rows:[['Granite Cookware Set',210,'premium'],['Trail Runner Jacket',139,'premium'],['Nimbus Headphones',129,'premium'],['Harbor Rain Shell',118,'premium'],['Summit Hiking Poles',96.4,'mid']]}},
    intro('Because CASE is an expression, the alias it gets is usable downstream. <span class="kbd-inline">ORDER BY band</span> works on the label you just produced, and <span class="kbd-inline">GROUP BY</span> can group by it too \u2014 the same pattern that turns a continuous number into a handful of categories you can count.'),
    {type:'exercise', id:'ex.f6.1',
      prompt:'Label every Outdoor product\'s stock. Return name, stock and a stock_level that is \'out\' when stock is 0, \'low\' when stock is under 20, otherwise \'ok\'. Order by stock ascending, then by name.',
      starter:"SELECT name, stock,\n       CASE -- out, low, otherwise ok\n       END AS stock_level\nFROM products\nWHERE category = 'Outdoor'\nORDER BY -- stock, then name",
      solution:"SELECT name, stock,\n       CASE WHEN stock = 0 THEN 'out'\n            WHEN stock < 20 THEN 'low'\n            ELSE 'ok' END AS stock_level\nFROM products\nWHERE category = 'Outdoor'\nORDER BY stock, name;",
      hints:['Test <span class="kbd-inline">stock = 0</span> before <span class="kbd-inline">stock &lt; 20</span>: the first match wins.','Close with <span class="kbd-inline">ELSE \'ok\'</span> so nothing is left as NULL.'],
      contract:{columns:['name','stock','stock_level'], column_order:'ignored', row_order:'exact'},
      result:{columns:['name','stock','stock_level'], rows:[['Discontinued Camp Stove',0,'out'],['Harbor Rain Shell',9,'low'],['Summit Hiking Poles',11,'low'],['Trail Runner Jacket',12,'low'],['Solar Camp Lantern',16,'low'],['Rope Dog Leash',35,'ok']]}}
  ]},

{ id:'f7', n:7, title:'Aggregation with HAVING', kind:'Foundations', minutes:16,
  summary:'Group rows, summarise each group, and filter the groups \u2014 not the rows \u2014 with HAVING.',
  objectives:['Aggregate rows per group with GROUP BY','Filter rows before grouping with WHERE and groups after with HAVING','Order groups by their aggregate'],
  blocks:[
    intro('<span class="kbd-inline">GROUP BY</span> collapses rows that share a key into one row per key, and an aggregate such as <span class="kbd-inline">COUNT(*)</span> summarises each group. The result has one row per group, so the aggregate only makes sense next to the grouping column it describes.'),
    intro('Filtering happens in two places. <span class="kbd-inline">WHERE</span> runs before the grouping and keeps or drops individual rows. <span class="kbd-inline">HAVING</span> runs after the grouping and keeps or drops whole groups. You cannot put an aggregate in <span class="kbd-inline">WHERE</span> \u2014 at that point the groups do not exist \u2014 and that is precisely the error <span class="kbd-inline">HAVING</span> exists to fix.'),
    {type:'syntax', sql:"SELECT category, COUNT(*) AS products\nFROM products\nGROUP BY category\nHAVING COUNT(*) > 4\nORDER BY products DESC, category;", note:'Group, summarise, then keep the groups whose count clears the threshold. The ORDER BY sorts the groups by their aggregate.'},
    {type:'example', id:'ex.f7.demo', sql:"SELECT category, COUNT(*) AS products\nFROM products\nGROUP BY category\nHAVING COUNT(*) > 4\nORDER BY products DESC, category;", note:'Kitchen has four products and drops out; the three categories with more survive.',
      result:{columns:['category','products'], rows:[['Outdoor',6],['Electronics',5],['Home',5]]}},
    intro('Read the two filters as a pipeline: <span class="kbd-inline">WHERE</span> chooses which rows are counted, <span class="kbd-inline">HAVING</span> chooses which counts you see. A filter on a raw column belongs in <span class="kbd-inline">WHERE</span> because it runs first and shrinks the work; a filter on <span class="kbd-inline">COUNT</span>, <span class="kbd-inline">SUM</span> or <span class="kbd-inline">AVG</span> belongs in <span class="kbd-inline">HAVING</span>.'),
    {type:'exercise', id:'ex.f7.1',
      prompt:'Return the number of orders per status, keeping only the statuses with at least 11 orders, most orders first; break ties alphabetically by status.',
      starter:"SELECT status, COUNT(*) AS orders\nFROM orders\nGROUP BY status\n-- keep the bigger groups\nORDER BY -- orders descending, then status",
      solution:"SELECT status, COUNT(*) AS orders\nFROM orders\nGROUP BY status\nHAVING COUNT(*) >= 11\nORDER BY orders DESC, status;",
      hints:['Group by <span class="kbd-inline">status</span> and count the rows in each group.','The threshold is on the aggregate, so it goes in <span class="kbd-inline">HAVING COUNT(*) &gt;= 11</span>, not in a <span class="kbd-inline">WHERE</span>.'],
      contract:{columns:['status','orders'], column_order:'ignored', row_order:'exact'},
      result:{columns:['status','orders'], rows:[['completed',37],['cancelled',11],['refunded',11],['shipped',11]]}}
  ]}
];

/* ---------- M5: the plan course (docs/m5-implementation-plan.md, step 7) ---------- */


var PLANS_MODULES: CourseModule[] = [
{ id:'p1', n:1, title:'The planner\u2019s job', kind:'Orientation', minutes:10,
  summary:'What PostgreSQL decides when you write a query, what its cost units mean, and why plan lessons need a bigger dataset than 80 rows.',
  objectives:['Describe the decisions the planner makes for a query','Read cost as pages and rows rather than milliseconds','Explain why the perf fixture holds 10 000 orders'],
  blocks:[
    intro('A <span class="kbd-inline">SELECT</span> says what you want; the planner decides how to get it \u2014 which relation to read first, whether to walk an index, which join algorithm to use, and whether to sort. <span class="kbd-inline">EXPLAIN</span> prints that decision, and this course is about reading it.'),
    intro('The decision is arithmetic: the planner prices candidate plans in page reads and row touches, using statistics that <span class="kbd-inline">ANALYZE</span> keeps about the data. Nothing about it is hidden \u2014 <span class="kbd-inline">pg_stats</span> shows the same numbers the planner used.'),
    intro('Plans only become interesting at scale. On the 80-row lesson dataset every plan is one page and every choice is the same; the perf fixture this course runs on holds 2 000 customers, 500 products, 10 000 orders and 20 000 order items, with a <span class="kbd-inline">status</span> column where one value is 90% of the table and another 0.5%.'),
    {type:'syntax', sql:"SELECT (SELECT COUNT(*) FROM customers)   AS customers,\n       (SELECT COUNT(*) FROM products)    AS products,\n       (SELECT COUNT(*) FROM orders)      AS orders,\n       (SELECT COUNT(*) FROM order_items) AS order_items;", note:'The perf fixture in one row. These counts are what every later estimate is a fraction of.'},
    {type:'example', id:'ex.p1.demo', sql:"SELECT (SELECT COUNT(*) FROM customers)   AS customers,\n       (SELECT COUNT(*) FROM products)    AS products,\n       (SELECT COUNT(*) FROM orders)      AS orders,\n       (SELECT COUNT(*) FROM order_items) AS order_items;", note:'Pinned by the fixture seed, so the same numbers come back on every machine.',
      result:{columns:['customers','products','orders','order_items'], rows:[[2000,500,10000,20000]]}},
    {type:'exercise', id:'ex.p1.1',
      prompt:'Return one row per table with its row count: the table name in a column called relation and the count in a column called table_rows, largest table first.',
      starter:"SELECT 'customers' AS relation, COUNT(*) AS table_rows FROM customers\n-- UNION ALL the other three tables\n-- order by table_rows descending",
      solution:"SELECT 'customers' AS relation, COUNT(*) AS table_rows FROM customers\nUNION ALL SELECT 'products', COUNT(*) FROM products\nUNION ALL SELECT 'orders', COUNT(*) FROM orders\nUNION ALL SELECT 'order_items', COUNT(*) FROM order_items\nORDER BY table_rows DESC;",
      hints:['Every branch of a <span class="kbd-inline">UNION ALL</span> projects the same two columns in the same order.','The names come from the first branch, so the trailing <span class="kbd-inline">ORDER BY table_rows DESC</span> can use the alias.'],
      contract:{columns:['relation','table_rows'], column_order:'exact', row_order:'exact'},
      result:{columns:['relation','table_rows'], rows:[['order_items',20000],['orders',10000],['customers',2000],['products',500]]}}
  ]},

{ id:'p2', n:2, title:'Reading EXPLAIN', kind:'Core skill', minutes:12,
  summary:'Read a plan as a tree: node types, depth, index conditions versus filters, estimates versus actual rows, and the numbers worth ignoring.',
  objectives:['Read a plan tree from the leaves up','Tell an Index Cond from a Filter','Multiply rows by loops before comparing estimates','Name the plan facts a fixture can reproduce'],
  blocks:[
    intro('A plan is a tree printed inside out: leaves read relations, parents combine them, and the root is the answer. <span class="kbd-inline">EXPLAIN</span> draws it with indentation, where depth is nesting rather than execution order.'),
    intro('The two lines that give a plan its character are <span class="kbd-inline">Index Cond</span>, which the index applied, and <span class="kbd-inline">Filter</span>, which discarded rows after they had been fetched. A large <span class="kbd-inline">Rows Removed by Filter</span> is work you paid for and did not want.'),
    intro('<span class="kbd-inline">EXPLAIN</span> only plans; <span class="kbd-inline">EXPLAIN ANALYZE</span> runs the statement and reports actual rows per node. Actual rows are per loop, so multiply by <span class="kbd-inline">loops=</span> before comparing them with anything.'),
    {type:'syntax', sql:"EXPLAIN (ANALYZE, BUFFERS)\nSELECT o.id, o.ordered_at, oi.product_id, oi.quantity\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nWHERE o.customer_id = 7;", note:'Plans only. Adding ANALYZE executes the statement and reports actual rows, buffers and timings per node.'},
    {type:'example', id:'ex.p2.demo', sql:"SELECT id, ordered_at, status\nFROM orders\nWHERE customer_id = 7\nORDER BY ordered_at DESC\nLIMIT 5;", note:'The query whose plan this module reads: customer 7 has 184 orders, and the index on customer_id finds them.',
      result:{columns:['id','ordered_at','status'], rows:[[9954,'2026-12-26 09:56','shipped'],[9899,'2026-12-24 10:16','shipped'],[9880,'2026-12-23 17:48','pending'],[9825,'2026-12-21 18:08','shipped'],[9751,'2026-12-19 02:00','shipped']]}},
    {type:'exercise', id:'ex.p2.1',
      prompt:'Return the five most recent orders for customer 1 as id, ordered_at and status, newest first.',
      starter:"SELECT id, ordered_at, status\nFROM orders\n-- filter to customer 1\nORDER BY -- newest first\nLIMIT 5;",
      solution:"SELECT id, ordered_at, status\nFROM orders\nWHERE customer_id = 1\nORDER BY ordered_at DESC\nLIMIT 5;",
      hints:['Filter with <span class="kbd-inline">customer_id = 1</span>.','Newest first is <span class="kbd-inline">ORDER BY ordered_at DESC</span>, and the <span class="kbd-inline">LIMIT</span> turns the sort into a top-N heapsort.'],
      contract:{columns:['id','ordered_at','status'], column_order:'exact', row_order:'exact'},
      result:{columns:['id','ordered_at','status'], rows:[[10000,'2026-12-28 01:48','shipped'],[9981,'2026-12-27 09:20','cancelled'],[9962,'2026-12-26 16:52','completed'],[9943,'2026-12-26 00:24','shipped'],[9926,'2026-12-25 09:40','shipped']]}}
  ]},

{ id:'p3', n:3, title:'Access paths', kind:'Core skill', minutes:12,
  summary:'Seq Scan, Index Scan, Index Only Scan and bitmap scans: what each costs, what it needs, and when a sequential scan is the correct plan.',
  objectives:['Name the four access paths and what each needs','Explain why an index pays off only when it lets you skip pages','Recognise the plan a 90% predicate should produce'],
  blocks:[
    intro('PostgreSQL has four ways to read a relation: <span class="kbd-inline">Seq Scan</span>, <span class="kbd-inline">Index Scan</span>, <span class="kbd-inline">Index Only Scan</span>, and <span class="kbd-inline">Bitmap Index Scan</span> plus <span class="kbd-inline">Bitmap Heap Scan</span>. Each trades pages for rows differently.'),
    intro('An index earns its keep by letting you skip pages. When a predicate matches most of the relation, the matching rows live on most of the pages anyway, and one sequential read is cheaper than a random walk over the same data \u2014 which is why the 90% <span class="kbd-inline">status</span> value wants a <span class="kbd-inline">Seq Scan</span> and the 0.5% value wants the index.'),
    intro('Correlation decides between the two index paths: near 1 (the column is stored in index order) favours <span class="kbd-inline">Index Scan</span>, near 0 favours <span class="kbd-inline">Bitmap Heap Scan</span>, which reads each heap page once, in page order.'),
    {type:'syntax', sql:"SELECT id, status\nFROM orders\nWHERE customer_id = 7\nORDER BY id\nLIMIT 5;", note:'A selective predicate on an indexed column: orders_customer_id_idx supplies 184 of the 10 000 rows.'},
    {type:'example', id:'ex.p3.demo', sql:"SELECT id, status\nFROM orders\nWHERE customer_id = 7\nORDER BY id\nLIMIT 5;", note:'The 184 rows for one customer are 1.8% of the table, which is where the index beats reading every page.',
      result:{columns:['id','status'], rows:[[28,'shipped'],[83,'shipped'],[102,'shipped'],[157,'shipped'],[231,'completed']]}},
    {type:'exercise', id:'ex.p3.1',
      prompt:'Return the five newest shipped orders as id, ordered_at and status, newest first. 90% of the table is shipped, so here the correct plan is a sequential scan rather than an index \u2014 the exercise asserts exactly that.',
      starter:"SELECT id, ordered_at, status\nFROM orders\nWHERE -- the shipped ones\nORDER BY -- newest first\nLIMIT 5;",
      solution:"SELECT id, ordered_at, status\nFROM orders\nWHERE status = 'shipped'\nORDER BY ordered_at DESC\nLIMIT 5;",
      hints:['The predicate is <span class="kbd-inline">status = \'shipped\'</span>, on the bare column so the statistics apply.','Newest first is <span class="kbd-inline">ORDER BY ordered_at DESC</span>. With nine thousand matching rows, reading the relation once beats visiting nine thousand heap pages through the index.'],
      contract:{columns:['id','ordered_at','status'], column_order:'exact', row_order:'exact'},
      perf:{engine:'postgresql',
        require_nodes:[{node_type:'Seq Scan', relation:'orders', label:'90% of the relation is cheaper to read once, in order, than to revisit page by page'}],
        forbid_nodes:[{node_type:'Index Scan', relation:'orders'},{node_type:'Bitmap Heap Scan', relation:'orders'}],
        min_index_scans:0},
      result:{columns:['id','ordered_at','status'], rows:[[10000,'2026-12-28 01:48','shipped'],[9999,'2026-12-28 00:56','shipped'],[9998,'2026-12-28 00:04','shipped'],[9997,'2026-12-27 23:12','shipped'],[9996,'2026-12-27 22:20','shipped']]}}
  ]},

{ id:'p4', n:4, title:'Selectivity, statistics and ANALYZE', kind:'Core skill', minutes:11,
  summary:'Where the rows= estimate comes from, the ways it goes wrong, how to tell, and what refreshing statistics changes.',
  objectives:['Read selectivity as a fraction of the relation','Use the MCV list and the histogram to explain an estimate','Spot an estimate that is wrong by comparing it with actual rows','Refresh statistics with ANALYZE after a bulk load'],
  blocks:[
    intro('Every estimate is a fraction of a relation: the fraction is <span class="kbd-inline">selectivity</span>, the row count comes from <span class="kbd-inline">reltuples</span>. Values that occur often are known exactly from the most-common-value list; the rest come from a histogram that assumes values are spread evenly inside each bucket, which is the assumption that breaks.'),
    intro('The estimate that misleads most is not a small error but a default. A predicate wrapped in a function or a cast has no statistic at all, so the planner assumes 0.5% of the relation \u2014 which is how a query matching 9 000 rows gets planned for 50 (module 7).'),
    intro('Estimates are falsifiable: <span class="kbd-inline">EXPLAIN ANALYZE</span> prints the claim next to the outcome on every node. A gap of an order of magnitude explains most bad plan choices, including a join algorithm chosen for rows that never existed (module 5).'),
    {type:'syntax', sql:"SELECT status, COUNT(*) AS orders\nFROM orders\nGROUP BY status\nORDER BY orders DESC;", note:'The skew that makes the perf fixture usable: one value covers 90% of the table, another 0.5%.'},
    {type:'example', id:'ex.p4.demo', sql:"SELECT status, COUNT(*) AS orders\nFROM orders\nGROUP BY status\nORDER BY orders DESC;", note:'Five distinct values, so every one of them sits in the MCV list and the planner estimates them exactly.',
      result:{columns:['status','orders'], rows:[['shipped',9000],['completed',400],['cancelled',300],['pending',250],['refunded',50]]}},
    {type:'exercise', id:'ex.p4.1',
      prompt:'Count the refunded orders. The value is 0.5% of the table, so the plan must go through orders_status_idx and the planner\'s estimate for the statement must stay under 100 rows.',
      starter:"SELECT COUNT(*) AS refunded\nFROM orders\nWHERE -- the rare status value",
      solution:"SELECT COUNT(*) AS refunded\nFROM orders\nWHERE status = 'refunded';",
      hints:['The predicate is <span class="kbd-inline">status = \'refunded\'</span>, on the bare column so the index and the statistics both apply.','One half of one percent of the table does not need a sequential read: the plan should touch orders_status_idx.'],
      contract:{columns:['refunded'], column_order:'exact', row_order:'ignored'},
      perf:{engine:'postgresql',
        require_indexes:['orders_status_idx'],
        min_index_scans:1,
        max_plan_rows:100,
        forbid_nodes:[{node_type:'Seq Scan', relation:'orders', above_plan_rows:1000, label:'a 50-row predicate is not a sequential scan on a 10 000-row relation'}]},
      result:{columns:['refunded'], rows:[[50]]}}
  ]},

{ id:'p5', n:5, title:'Join strategies', kind:'Core skill', minutes:11,
  summary:'Nested loop, hash join and merge join: what each needs, what each costs, and how a wrong row estimate switches the choice.',
  objectives:['State the precondition of each join algorithm','Read loops= on the inner side of a nested loop','Explain why a row estimate changes the join strategy'],
  blocks:[
    intro('A join is a matching problem with three solutions: <span class="kbd-inline">Nested Loop</span>, cheap when the outer side is small and the inner side is indexed; <span class="kbd-inline">Hash Join</span>, for two large unordered inputs and equality only; and <span class="kbd-inline">Merge Join</span>, for two inputs already sorted on the key.'),
    intro('The outer side\u2019s estimate picks the strategy. A nested loop is the only join that costs what the answer costs; a hash join pays one pass over each side however few rows come back. When the estimate is wrong (module 4), the planner picks the strategy its belief justified.'),
    intro('<span class="kbd-inline">EXISTS</span> and <span class="kbd-inline">IN</span> become semi joins that stop at the first match, and <span class="kbd-inline">NOT EXISTS</span> becomes an anti join. That early exit is part of the plan, and it is why they beat joining and then de-duplicating.'),
    {type:'syntax', sql:"SELECT c.name, COUNT(*) AS orders\nFROM customers c\nJOIN orders o ON o.customer_id = c.id\nGROUP BY c.id, c.name\nORDER BY orders DESC, c.name\nLIMIT 5;", note:'Both sides are large and unordered, so the planner hashes one side and streams the other; the aggregate hashes as well.'},
    {type:'example', id:'ex.p5.demo', sql:"SELECT c.name, COUNT(*) AS orders\nFROM customers c\nJOIN orders o ON o.customer_id = c.id\nGROUP BY c.id, c.name\nORDER BY orders DESC, c.name\nLIMIT 5;", note:'A skewed customer_id leaves a handful of customers with hundreds of orders; one hash join over 10 000 orders finds them in a single pass.',
      result:{columns:['name','orders'], rows:[['Ada Whitfield',490],['Bruno Sala',430],['Chidi Okafor',400],['Dara Novak',360],['Emi Tanaka',340]]}},
    {type:'exercise', id:'ex.p5.1',
      prompt:'Return every product category with the number of order lines that reference a product in it, most lines first. A category with no lines must still appear, with a count of zero.',
      starter:"SELECT p.category, COUNT(oi.id) AS lines\nFROM products p\n-- keep every category\nJOIN order_items oi ON -- the product link\nGROUP BY p.category\nORDER BY -- most lines first",
      solution:"SELECT p.category, COUNT(oi.id) AS lines\nFROM products p\nLEFT JOIN order_items oi ON oi.product_id = p.id\nGROUP BY p.category\nORDER BY lines DESC, p.category;",
      hints:['A <span class="kbd-inline">LEFT JOIN</span> keeps a category that has no lines, and <span class="kbd-inline">COUNT(oi.id)</span> then counts zero for it instead of one.','Order by the aggregate, with a tie-break so the output is stable: <span class="kbd-inline">ORDER BY lines DESC, p.category</span>.'],
      contract:{columns:['category','lines'], column_order:'ignored', row_order:'exact'},
      result:{columns:['category','lines'], rows:[['Electronics',5705],['Home',4923],['Outdoor',4831],['Kitchen',4541]]}}
  ]},

{ id:'p6', n:6, title:'Sorts, hashes and work_mem', kind:'Core skill', minutes:11,
  summary:'Sort and hash nodes, what work_mem bounds, how to read a spill, and the order of fixes that avoids one.',
  objectives:['Read Sort Method and Disk: on a Sort node','Tell a hashed aggregate from a sorted one','Reduce rows, width or the number of sorts before raising work_mem'],
  blocks:[
    intro('A <span class="kbd-inline">Sort</span> emits nothing until it has read everything, so its startup cost is its total cost. A <span class="kbd-inline">LIMIT</span> rescues it: a bounded sort becomes a top-N heapsort that keeps N rows instead of all of them.'),
    intro('<span class="kbd-inline">work_mem</span> bounds one sort or one hash at a time, per session. When the input does not fit, a sort becomes an <span class="kbd-inline">external merge</span> with a <span class="kbd-inline">Disk:</span> figure, and a hash join or hashed aggregate spills in batches \u2014 both of them visible in the plan.'),
    intro('The fixes, in the order that usually works: sort fewer rows (a sargable predicate), sort narrower rows (project the columns you need), sort less often (<span class="kbd-inline">UNION ALL</span> instead of <span class="kbd-inline">UNION</span>, <span class="kbd-inline">EXISTS</span> instead of a join plus <span class="kbd-inline">DISTINCT</span>), and only then raise <span class="kbd-inline">work_mem</span>.'),
    {type:'syntax', sql:"SELECT id, ordered_at, status\nFROM orders\nORDER BY ordered_at DESC\nLIMIT 5;", note:'Nothing indexes ordered_at, so the plan is a sequential scan under a Sort \u2014 reduced to a top-N heapsort by the LIMIT.'},
    {type:'example', id:'ex.p6.demo', sql:"SELECT id, ordered_at, status\nFROM orders\nORDER BY ordered_at DESC\nLIMIT 5;", note:'The five newest orders of any status. The Sort Method line reads top-N heapsort: only five rows are ever held in memory.',
      result:{columns:['id','ordered_at','status'], rows:[[10000,'2026-12-28 01:48','shipped'],[9999,'2026-12-28 00:56','shipped'],[9998,'2026-12-28 00:04','shipped'],[9997,'2026-12-27 23:12','shipped'],[9996,'2026-12-27 22:20','shipped']]}},
    {type:'exercise', id:'ex.p6.1',
      prompt:'Return the three products that appear in the most order lines, with their id, name and line count; break ties by id ascending.',
      starter:"SELECT p.id, p.name, COUNT(*) AS lines\nFROM order_items oi\nJOIN products p ON -- the product link\nGROUP BY -- one row per product\nORDER BY -- most lines first, then id\nLIMIT 3;",
      solution:"SELECT p.id, p.name, COUNT(*) AS lines\nFROM order_items oi\nJOIN products p ON p.id = oi.product_id\nGROUP BY p.id, p.name\nORDER BY lines DESC, p.id\nLIMIT 3;",
      hints:['Group by <span class="kbd-inline">p.id, p.name</span> so one row per product keeps the name available.','The grouping is a hash and the ranking is a sort on top of it: <span class="kbd-inline">ORDER BY lines DESC, p.id</span>.'],
      contract:{columns:['id','name','lines'], column_order:'exact', row_order:'exact'},
      result:{columns:['id','name','lines'], rows:[[1,'Aurora Wireless Earbuds',895],[2,'Nimbus Headphones',370],[3,'Linen Duvet Cover',285]]}}
  ]},

{ id:'p7', n:7, title:'Making a predicate sargable', kind:'Core skill', minutes:12,
  summary:'Functions, casts, OR, LIKE prefixes and IN versus EXISTS: the rewrites that let an index seek, and the ones that do nothing.',
  objectives:['Recognise a predicate an index can seek with','Rewrite a wrapped column into a comparison on the bare column','Tell the IN and EXISTS plans apart, and the NOT IN trap'],
  blocks:[
    intro('An index can seek only when one side of the comparison is the bare column. Any function or cast around that column hides it: the index is still there, the predicate is still correct, and the plan becomes a <span class="kbd-inline">Seq Scan</span> with a <span class="kbd-inline">Filter</span>.'),
    intro('The damage is doubled, because a wrapped column also has no statistic: the estimate falls back to 0.5% of the relation (module 4). One cast buys the wrong access path and the wrong row count. <span class="kbd-inline">DATE(ordered_at) = ...</span> becomes a half-open range, and <span class="kbd-inline">customer_id::text = \'7\'</span> becomes <span class="kbd-inline">customer_id = 7</span>.'),
    intro('<span class="kbd-inline">OR</span> and <span class="kbd-inline">IN</span> are fine while every branch is indexable, because the planner can build a <span class="kbd-inline">BitmapOr</span>, and expensive when one branch is not. <span class="kbd-inline">LIKE \'Aurora%\'</span> needs the right operator class before it is a range; a leading wildcard has no bound at all.'),
    {type:'syntax', sql:"SELECT id, name\nFROM products\nWHERE name LIKE 'Aurora%';", note:'An anchored pattern, and still a sequential scan: a plain B-tree index on name cannot serve LIKE unless the index uses text_pattern_ops or the C collation.'},
    {type:'example', id:'ex.p7.demo', sql:"SELECT id, name\nFROM products\nWHERE name LIKE 'Aurora%';", note:'One row out of 500, found by reading all 500 \u2014 the concrete case that needs an index built for the pattern, not a bigger index on the column.',
      result:{columns:['id','name'], rows:[[1,'Aurora Wireless Earbuds']]}},
    {type:'exercise', id:'ex.p7.1',
      prompt:'The support console receives the customer id as text, and the starter casts the column to compare it. Rewrite the predicate so the plan can use orders_customer_id_idx, and return the customer\'s five oldest orders as id, ordered_at and status.',
      starter:"SELECT id, ordered_at, status\nFROM orders\nWHERE customer_id::text = '7'\nORDER BY ordered_at\nLIMIT 5;",
      solution:"SELECT id, ordered_at, status\nFROM orders\nWHERE customer_id = 7\nORDER BY ordered_at\nLIMIT 5;",
      hints:['Compare the bare column with a typed literal: <span class="kbd-inline">customer_id = 7</span>. A cast on the constant is free; a cast on the column is not.','Nothing indexes <span class="kbd-inline">ordered_at</span>, so the planner cannot stream the answer in order \u2014 it has to collect every row the predicate matched before it can sort them. Reading 184 rows out of 10 000 is what orders_customer_id_idx is for, and the cast takes it away: the wrapped column has no statistic either, so the estimate falls back to 50 rows and the plan reads the whole relation twice over.'],
      contract:{columns:['id','ordered_at','status'], column_order:'exact', row_order:'exact'},
      perf:{engine:'postgresql',
        require_indexes:['orders_customer_id_idx'],
        min_index_scans:1,
        forbid_nodes:[{node_type:'Seq Scan', relation:'orders', above_plan_rows:1000, label:'the set is 1.8% of the relation, so the index has to be the way in'}]},
      result:{columns:['id','ordered_at','status'], rows:[[28,'2026-01-01 23:24','shipped'],[83,'2026-01-03 23:04','shipped'],[102,'2026-01-04 15:32','shipped'],[157,'2026-01-06 15:12','shipped'],[231,'2026-01-09 07:20','completed']]}}
  ]},

{ id:'p8', n:8, title:'Capstone: a slow query, read end to end', kind:'Advanced', minutes:11,
  summary:'Read a slow order-detail query, name the flaw, rewrite the predicate, and prove the fix with plan structure instead of a stopwatch.',
  objectives:['Diagnose a plan by its Filter and its Rows Removed','Rewrite a key predicate so the primary key can seek','Assert a plan structurally rather than by timing'],
  blocks:[
    intro('The report is an order detail page: one order and its lines, with the order id arriving from the web layer as text. The starter compares that text against <span class="kbd-inline">id::text</span>, which hides the primary key, so the plan reads all 10 000 orders, discards 9 999 of them with a <span class="kbd-inline">Filter</span>, and reads the 20 000 order lines to find two.'),
    intro('The fix is two predicate edits: compare the bare <span class="kbd-inline">o.id</span> with a typed literal so <span class="kbd-inline">orders_pkey</span> can seek, and filter <span class="kbd-inline">oi.order_id</span> by the same value so the inner side of the join is an index lookup rather than a full read. The same rows come back, a handful of rows are touched.'),
    intro('The exercise is graded on structure \u2014 the indexes used, the absence of a sequential scan above a row threshold, the planner\u2019s own estimate \u2014 because those facts are identical on every machine, while a duration is not.'),
    {type:'syntax', sql:"EXPLAIN (ANALYZE, BUFFERS)\nSELECT o.id AS order_id, o.ordered_at, oi.product_id, oi.quantity\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nWHERE o.id = 1050\n  AND oi.order_id = 1050\nORDER BY oi.product_id;", note:'The capstone query: two index seeks feed a nested loop, and the estimate is a handful of rows rather than ten thousand.'},
    {type:'example', id:'ex.p8.demo', sql:"SELECT (SELECT COUNT(*) FROM orders WHERE id = 1050) AS orders_matched,\n       (SELECT COUNT(*) FROM order_items WHERE order_id = 1050) AS lines_matched;", note:'How much of the fixture the report actually needs: one order and its two lines, out of 10 000 and 20 000.',
      result:{columns:['orders_matched','lines_matched'], rows:[[1,2]]}},
    {type:'exercise', id:'ex.p8.1',
      prompt:'Rewrite the starter so the plan finds the order by primary key and its lines by index. Return order_id, ordered_at, product_id and quantity for order 1050, ordered by product_id. Both sequential scans must be gone, the estimate must stay at a handful of rows, and at least two index scans must appear.',
      starter:"SELECT o.id AS order_id, o.ordered_at, oi.product_id, oi.quantity\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nWHERE o.id::text = '1050'\nORDER BY oi.product_id;",
      solution:"SELECT o.id AS order_id, o.ordered_at, oi.product_id, oi.quantity\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nWHERE o.id = 1050\n  AND oi.order_id = 1050\nORDER BY oi.product_id;",
      hints:['Compare <span class="kbd-inline">o.id</span> with the number 1050, not with text cast through the column.','Add <span class="kbd-inline">AND oi.order_id = 1050</span> so the join\u2019s inner side is a seek on order_items_order_id_idx instead of a sequential read of all 20 000 lines.'],
      contract:{columns:['order_id','ordered_at','product_id','quantity'], column_order:'exact', row_order:'exact'},
      perf:{engine:'postgresql',
        require_nodes:[{node_type:'Index Scan', relation:'orders', index:'orders_pkey', label:'the order is found by its primary key'}],
        forbid_nodes:[{node_type:'Seq Scan', relation:'orders', above_plan_rows:100},{node_type:'Seq Scan', relation:'order_items', above_plan_rows:100}],
        require_indexes:['orders_pkey','order_items_order_id_idx'],
        max_plan_rows:20,
        min_index_scans:2},
      result:{columns:['order_id','ordered_at','product_id','quantity'], rows:[[1050,'2026-02-07 21:08',5,3],[1050,'2026-02-07 21:08',123,1]]}}
  ]}
];


/* M6: the SQLite track. Three courses the app ships that teach SQLite on its
   own terms; every chapter carries `dialect: 'sqlite'` (D15: a chapter teaches
   exactly one dialect, and a cross-dialect comparison is its own chapter).
   These chapters run on the SQLite dataset, a row-for-row mirror of the
   commerce schema in `backend/internal/dataset/schema.sqlite.sql`. */

var SQLITE_ESSENTIALS_MODULES: CourseModule[] = [
{ id:'s1', n:1, title:'A database in a file', kind:'Orientation', minutes:12, dialect:'sqlite',
  summary:'The file is the database: no server, no roles, no search_path, and a catalog you query like any other table.',
  objectives:['Explain what a SQLite database actually is on disk','Read the schema from sqlite_master','Say where the security boundary sits when there is no server'],
  blocks:[
    {type:'syntax', sql:"SELECT type, name\nFROM sqlite_master\nWHERE type IN ('table', 'index')\nORDER BY type DESC, name;", note:'The whole schema, read with ordinary SELECT: rows for tables and indexes, and the CREATE statement that made each one in the sql column.'},
    {type:'example', id:'ex.s1.demo', sql:"SELECT type, name\nFROM sqlite_master\nWHERE type IN ('table', 'index')\nORDER BY type DESC, name;", note:'Five tables, five indexes, one file. There is no server to ask and no \u201cdatabase\u201d argument in the query \u2014 the connection already is the database.',
      result:{columns:['type','name'], rows:[['table','customers'],['table','dataset_meta'],['table','order_items'],['table','orders'],['table','products'],['index','order_items_order_id_idx'],['index','order_items_product_id_idx'],['index','orders_customer_id_idx'],['index','orders_status_idx'],['index','sqlite_autoindex_dataset_meta_1']]}},
    {type:'exercise', id:'ex.s1.1',
      prompt:'Return every index stored in this file as a single column named name, alphabetically. (The engine creates some of them without being asked \u2014 include those.)',
      starter:"SELECT -- one column, named name\nFROM sqlite_master\nWHERE -- only indexes\nORDER BY -- alphabetically",
      solution:"SELECT name\nFROM sqlite_master\nWHERE type = 'index'\nORDER BY name;",
      hints:['Indexes are rows in <span class="kbd-inline">sqlite_master</span> with <span class="kbd-inline">type = \'index\'</span>.','One of the five comes from the <span class="kbd-inline">dataset_meta</span> primary key \u2014 SQLite named it <span class="kbd-inline">sqlite_autoindex_...</span> for you.'],
      contract:{columns:['name'], column_order:'exact', row_order:'exact'},
      result:{columns:['name'], rows:[['order_items_order_id_idx'],['order_items_product_id_idx'],['orders_customer_id_idx'],['orders_status_idx'],['sqlite_autoindex_dataset_meta_1']]}}
  ]},

{ id:'s2', n:2, title:'Storage classes and type affinity', kind:'Core skill', minutes:16, dialect:'sqlite',
  summary:'A declared type picks an affinity, not a constraint; typeof() reads the storage class a value actually has.',
  objectives:['Name the five storage classes and the five affinity rules','Explain why a NUMERIC column can store an INTEGER','Measure what is stored with typeof()','Cast with CAST, since there is no ::'],
  blocks:[
    {type:'syntax', sql:"SELECT id, name, price, typeof(price) AS stored\nFROM products\nORDER BY id\nLIMIT 5;", note:'The declared type is NUMERIC; the stored class is whatever the value turned out to be. typeof() is how you read it.'},
    {type:'example', id:'ex.s2.demo', sql:"SELECT typeof(price) AS stored, COUNT(*) AS products\nFROM products\nGROUP BY stored\nORDER BY stored;", note:'One declared type, two storage classes: NUMERIC affinity turned \u201859.00\u2019 into the integer 59 and \u201842.50\u2019 into the real 42.5. PostgreSQL would report numeric for all twenty.',
      result:{columns:['stored','products'], rows:[['integer',10],['real',10]]}},
    {type:'exercise', id:'ex.s2.1',
      prompt:'Return the id, name and storage class of every product whose price is stored as a REAL, ordered by id. Call the class column stored.',
      starter:"SELECT id, name,\n       -- storage class of price\nFROM products\nWHERE -- only the reals\nORDER BY id;",
      solution:"SELECT id, name,\n       typeof(price) AS stored\nFROM products\nWHERE typeof(price) = 'real'\nORDER BY id;",
      hints:['<span class="kbd-inline">typeof(price)</span> returns the storage class as text: \'integer\', \'real\', \'text\', \'blob\' or \'null\'.','A two-decimal seed value keeps its fraction only when the fraction is non-zero, so filter on the class rather than on the declared type.'],
      contract:{columns:['id','name','stored'], column_order:'exact', row_order:'exact'},
      result:{columns:['id','name','stored'], rows:[[4,'Cedar Desk Lamp','real'],[6,'Pulse Fitness Tracker','real'],[7,'Vertex Mechanical Keyboard','real'],[10,'Copper Kettle','real'],[13,'Summit Hiking Poles','real'],[14,'Ceramic Vase','real'],[15,'Espresso Tamper','real'],[17,'Bamboo Shelf','real'],[18,'Noise-Cancel Earmuffs','real'],[19,'Rope Dog Leash','real']]}}
  ]},

{ id:'s3', n:3, title:'rowid, INTEGER PRIMARY KEY and AUTOINCREMENT', kind:'Core skill', minutes:14, dialect:'sqlite',
  summary:'Every ordinary row has an address; INTEGER PRIMARY KEY is that address, and AUTOINCREMENT is a promise about reuse.',
  objectives:['Select the rowid and know its aliases','Explain why INTEGER PRIMARY KEY is the rowid, not a copy of it','Choose between INTEGER PRIMARY KEY and AUTOINCREMENT'],
  blocks:[
    {type:'syntax', sql:"SELECT rowid AS rid, id, name\nFROM products\nORDER BY id\nLIMIT 3;", note:'id INTEGER PRIMARY KEY is an alias for the rowid, so rid and id are the same number read two ways.'},
    {type:'example', id:'ex.s3.demo', sql:"SELECT MAX(id) AS max_id, MAX(rowid) AS max_rowid, COUNT(*) AS rows\nFROM products;", note:'Twenty products, ids 1..20, and the rowid agrees: the ids are dense because the id is the key. A table with a gap-free id space is evidence of an INTEGER PRIMARY KEY alias.',
      result:{columns:['max_id','max_rowid','rows'], rows:[[20,20,20]]}},
    {type:'exercise', id:'ex.s3.1',
      prompt:'Return the rowid, the id and the order_id of the three highest-id order_items, highest id first. Call the rowid column rid.',
      starter:"SELECT rowid AS rid, id, order_id\nFROM order_items\nORDER BY -- highest first\nLIMIT 3;",
      solution:"SELECT rowid AS rid, id, order_id\nFROM order_items\nORDER BY id DESC\nLIMIT 3;",
      hints:['Say <span class="kbd-inline">rowid AS rid</span> so the two address columns have distinct names.','<span class="kbd-inline">MAX(rowid)</span> is 160, so <span class="kbd-inline">ORDER BY id DESC</span> starts there.'],
      contract:{columns:['rid','id','order_id'], column_order:'exact', row_order:'exact'},
      result:{columns:['rid','id','order_id'], rows:[[160,160,1080],[159,159,1080],[158,158,1079]]}}
  ]},

{ id:'s4', n:4, title:'Dates are not a type', kind:'Core skill', minutes:16, dialect:'sqlite',
  summary:'ISO-8601 text is the storage format; date(), datetime(), strftime(), julianday() and unixepoch() are the calendar.',
  objectives:['Store and read an ISO-8601 date as TEXT','Truncate a timestamp with strftime instead of DATE_TRUNC','Do date arithmetic with modifier strings'],
  blocks:[
    {type:'syntax', sql:"SELECT strftime('%Y-%m', signup_date) AS month,\n       COUNT(*) AS signups\nFROM customers\nGROUP BY month\nORDER BY month;", note:'DATE_TRUNC(\u2019month\u2019, signup_date) in PostgreSQL; a format string in SQLite. Same truncation, different mechanism.'},
    {type:'example', id:'ex.s4.demo', sql:"SELECT strftime('%Y-%m-01', ordered_at) AS month,\n       COUNT(*) AS orders\nFROM orders\nGROUP BY month\nORDER BY month;", note:'The first of each month, as text. Because the text is ISO-8601 it also sorts chronologically, so GROUP BY and ORDER BY on it behave like a date column would.',
      result:{columns:['month','orders'], rows:[['2026-01-01',16],['2026-02-01',19],['2026-03-01',18],['2026-04-01',18],['2026-05-01',9]]}},
    {type:'exercise', id:'ex.s4.1',
      prompt:'Return the number of customers who signed up in each month of 2026 as month (YYYY-MM) and signups, months ascending.',
      starter:"SELECT strftime('%Y-%m', signup_date) AS month,\n       COUNT(*) AS signups\nFROM customers\nWHERE -- 2026 signups only\nGROUP BY month\nORDER BY month;",
      solution:"SELECT strftime('%Y-%m', signup_date) AS month,\n       COUNT(*) AS signups\nFROM customers\nWHERE signup_date >= '2026-01-01'\nGROUP BY month\nORDER BY month;",
      hints:['A date stored as text compares as text: <span class="kbd-inline">signup_date &gt;= \'2026-01-01\'</span> works because the format is fixed.','<span class="kbd-inline">strftime(\'%Y-%m\', ...)</span> gives the year and month; group by the alias.'],
      contract:{columns:['month','signups'], column_order:'exact', row_order:'exact'},
      result:{columns:['month','signups'], rows:[['2026-01',2],['2026-02',3],['2026-03',3],['2026-04',2],['2026-05',3],['2026-06',3],['2026-07',3]]}}
  ]},

{ id:'s5', n:5, title:'LIKE, GLOB and collations', kind:'Core skill', minutes:14, dialect:'sqlite',
  summary:'LIKE folds ASCII case by default, GLOB never does, and there is no ILIKE because LIKE already is one.',
  objectives:['Predict which rows LIKE matches, including its case folding','Use GLOB and its wildcard syntax','Explain what COLLATE NOCASE changes and its ASCII limit'],
  blocks:[
    {type:'syntax', sql:"SELECT 'Ada' LIKE 'a%' AS like_ci,\n       'Ada' GLOB 'a*' AS glob_cs;", note:'LIKE folds case, GLOB does not. Both answer 1/0 because there is no boolean type.'},
    {type:'example', id:'ex.s5.demo', sql:"SELECT name\nFROM products\nWHERE name GLOB '*Jacket*'\nORDER BY name;", note:'GLOB is case-sensitive and its wildcard is *; a product whose name spells Jacket with a capital J is the only match.',
      result:{columns:['name'], rows:[['Trail Runner Jacket']]}},
    {type:'exercise', id:'ex.s5.1',
      prompt:'Compare the two operators on the same pattern: count the products whose name matches \'%AN%\' with LIKE as like_an and with GLOB as glob_an, in that order.',
      starter:"SELECT (SELECT COUNT(*) FROM products WHERE name LIKE '%AN%') AS like_an,\n       (SELECT COUNT(*) FROM products WHERE name GLOB '*AN*') AS glob_an;",
      solution:"SELECT (SELECT COUNT(*) FROM products WHERE name LIKE '%AN%') AS like_an,\n       (SELECT COUNT(*) FROM products WHERE name GLOB '*AN*') AS glob_an;",
      hints:['LIKE folds ASCII case, so <span class="kbd-inline">\'%AN%\'</span> finds names spelled with lower-case <span class="kbd-inline">an</span>.','GLOB is exact-case and uses <span class="kbd-inline">*</span>, not <span class="kbd-inline">%</span>, for a run of characters.'],
      contract:{columns:['like_an','glob_an'], column_order:'exact', row_order:'ignored'},
      result:{columns:['like_an','glob_an'], rows:[[5,0]]}}
  ]},

{ id:'s6', n:6, title:'Reading a query plan without EXPLAIN ANALYZE', kind:'Core skill', minutes:15, dialect:'sqlite',
  summary:'EXPLAIN QUERY PLAN prints loops and index use; ANALYZE writes statistics; nothing measures a running query.',
  objectives:['Tell EXPLAIN from EXPLAIN QUERY PLAN from ANALYZE','Read SCAN and SEARCH lines and the index they name','Spot a predicate that hides an index from the planner'],
  blocks:[
    {type:'syntax', sql:"EXPLAIN QUERY PLAN\nSELECT id FROM orders WHERE status = 'refunded';", note:'A plan is rows, not prose: SEARCH means an index seek, SCAN a full pass. There is no EXPLAIN ANALYZE in SQLite.'},
    {type:'example', id:'ex.s6.demo', sql:"SELECT id, status, customer_id\nFROM orders\nWHERE customer_id = 1\nORDER BY id;", note:'The query whose plan the chapter reads: an equality on customer_id becomes SEARCH o USING COVERING INDEX orders_customer_id_idx (customer_id=?), and a covering index means the table is never visited.',
      result:{columns:['id','status','customer_id'], rows:[[1001,'completed',1],[1002,'completed',1],[1005,'completed',1],[1006,'shipped',1],[1007,'refunded',1]]}},
    {type:'exercise', id:'ex.s6.1',
      prompt:'Return the id, status and customer_id of every order placed by customer 3, lowest id first. Then run EXPLAIN QUERY PLAN on your statement and find the index it searches.',
      starter:"SELECT id, status, customer_id\nFROM orders\nWHERE -- customer 3\nORDER BY -- lowest id first;",
      solution:"SELECT id, status, customer_id\nFROM orders\nWHERE customer_id = 3\nORDER BY id;",
      hints:['The predicate is <span class="kbd-inline">customer_id = 3</span>, on the bare column, so <span class="kbd-inline">orders_customer_id_idx</span> can serve it.','Lowest id first is <span class="kbd-inline">ORDER BY id</span> with no DESC.'],
      contract:{columns:['id','status','customer_id'], column_order:'exact', row_order:'exact'},
      result:{columns:['id','status','customer_id'], rows:[[1004,'completed',3],[1009,'completed',3],[1010,'completed',3],[1011,'pending',3]]}}
  ]},

{ id:'s7', n:7, title:'When SQLite is the right choice \u2014 and what it cannot do', kind:'Orientation', minutes:12, dialect:'sqlite',
  summary:'One file, one writer, no roles and no schemas: where that fits, where it does not, and what you give up.',
  objectives:['Recognise the workloads SQLite is the right store for','Name the five features it genuinely lacks','Read the whole store in one query'],
  blocks:[
    {type:'syntax', sql:"SELECT (SELECT COUNT(*) FROM customers)   AS customers,\n       (SELECT COUNT(*) FROM products)    AS products,\n       (SELECT COUNT(*) FROM orders)      AS orders,\n       (SELECT COUNT(*) FROM order_items) AS order_items;", note:'Four relations, one row, one file. Copy the file and you have copied all of it.'},
    {type:'example', id:'ex.s7.demo', sql:"SELECT (SELECT COUNT(*) FROM customers)   AS customers,\n       (SELECT COUNT(*) FROM products)    AS products,\n       (SELECT COUNT(*) FROM orders)      AS orders,\n       (SELECT COUNT(*) FROM order_items) AS order_items;", note:'The whole commerce store in a single row \u2014 the positive case for a database that is a file.',
      result:{columns:['customers','products','orders','order_items'], rows:[[30,20,80,160]]}},
    {type:'exercise', id:'ex.s7.1',
      prompt:'Return one row per commerce table with the table name as relation and its row count as table_rows, largest table first.',
      starter:"SELECT 'customers' AS relation, COUNT(*) AS table_rows FROM customers\n-- UNION ALL the other three\n-- order by table_rows descending",
      solution:"SELECT 'customers' AS relation, COUNT(*) AS table_rows FROM customers\nUNION ALL SELECT 'products', COUNT(*) FROM products\nUNION ALL SELECT 'orders', COUNT(*) FROM orders\nUNION ALL SELECT 'order_items', COUNT(*) FROM order_items\nORDER BY table_rows DESC;",
      hints:['Every branch of a <span class="kbd-inline">UNION ALL</span> projects the same two columns in the same order.','The names come from the first branch, so the trailing <span class="kbd-inline">ORDER BY table_rows DESC</span> can use the alias.'],
      contract:{columns:['relation','table_rows'], column_order:'exact', row_order:'exact'},
      result:{columns:['relation','table_rows'], rows:[['order_items',160],['orders',80],['customers',30],['products',20]]}}
  ]}
];


var SQLITE_IN_PRODUCTION_MODULES: CourseModule[] = [
{ id:'r1', n:1, title:'One writer at a time: concurrency and WAL', kind:'Orientation', minutes:16, dialect:'sqlite',
  summary:'WAL lets readers share the file with one writer; SQLITE_BUSY is what the second writer meets, and busy_timeout is the retry.',
  objectives:['Explain why SQLite serialises writers and what WAL changes','Read the journal mode and the busy timeout from the file and the connection','Say what a retry after SQLITE_BUSY has to re-run'],
  blocks:[
    {type:'syntax', sql:'PRAGMA journal_mode;', note:'A statement that returns rows. delete is the default and it lives in the file header; wal is the mode a read-heavy application wants.'},
    {type:'example', id:'ex.r1.demo', sql:'PRAGMA journal_mode;', note:'This fixture is a read-only copy, so it reports delete: switching to WAL rewrites the header and has to create a -wal file beside the database.',
      result:{columns:['journal_mode'], rows:[['delete']]}},
    {type:'exercise', id:'ex.r1.1',
      prompt:'Return the two concurrency settings in one row: the file\u2019s journal_mode and the connection\u2019s busy_timeout in milliseconds. Use the table-valued pragmas, not the bare PRAGMA statements.',
      starter:"SELECT (SELECT -- the journal mode\n       ) AS journal_mode,\n       (SELECT -- the busy timeout\n       ) AS busy_timeout;",
      solution:"SELECT (SELECT journal_mode FROM pragma_journal_mode()) AS journal_mode,\n       (SELECT timeout FROM pragma_busy_timeout()) AS busy_timeout;",
      hints:['<span class="kbd-inline">pragma_journal_mode()</span> and <span class="kbd-inline">pragma_busy_timeout()</span> are tables, so each can be a scalar subquery.','The busy-timeout column is called <span class="kbd-inline">timeout</span>, not <span class="kbd-inline">busy_timeout</span>.'],
      contract:{columns:['journal_mode','busy_timeout'], column_order:'exact', row_order:'ignored'},
      result:{columns:['journal_mode','busy_timeout'], rows:[['delete',2000]]}}
  ]},

{ id:'r2', n:2, title:'PRAGMA vs SET', kind:'Core skill', minutes:14, dialect:'sqlite',
  summary:'A pragma is SQL that returns rows; the read-only pragmas are also tables you can join, and half of them are per-connection.',
  objectives:['Read a schema through pragma_table_info','Tell a file-level pragma from a connection-level one','Say why the executor sets its pragmas where it opens the connection'],
  blocks:[
    {type:'syntax', sql:'PRAGMA table_info(products);', note:'Not shell output: a result set with the columns cid, name, type, notnull, dflt_value and pk, one row per column of the table.'},
    {type:'example', id:'ex.r2.demo', sql:"SELECT cid, name, type, \"notnull\", dflt_value, pk\nFROM pragma_table_info('products')\nORDER BY cid;", note:'The table-valued spelling is ordinary SQL, so it can be filtered, joined and ordered like any other table. The same read through the shell would have been a one-off print.',
      result:{columns:['cid','name','type','notnull','dflt_value','pk'], rows:[[0,'id','INTEGER',0,null,1],[1,'name','TEXT',1,null,0],[2,'category','TEXT',1,null,0],[3,'price','NUMERIC',1,null,0],[4,'stock','INTEGER',1,'0',0],[5,'active','BOOLEAN',1,'1',0]]}},
    {type:'exercise', id:'ex.r2.1',
      prompt:'For every table in the file, return its name as table_name and how many columns it holds as columns, alphabetically.',
      starter:"SELECT m.name AS table_name,\n       (SELECT -- count the columns\n       ) AS columns\nFROM sqlite_master m\nWHERE m.type = 'table'\nORDER BY m.name;",
      solution:"SELECT m.name AS table_name,\n       (SELECT COUNT(*) FROM pragma_table_info(m.name)) AS columns\nFROM sqlite_master m\nWHERE m.type = 'table'\nORDER BY m.name;",
      hints:['<span class="kbd-inline">pragma_table_info()</span> takes the table name as an argument, and an outer column is a fine argument.','<span class="kbd-inline">COUNT(*)</span> over that pragma counts the columns of the table it was handed.'],
      contract:{columns:['table_name','columns'], column_order:'exact', row_order:'exact'},
      result:{columns:['table_name','columns'], rows:[['customers',5],['dataset_meta',2],['order_items',5],['orders',4],['products',6]]}}
  ]},

{ id:'r3', n:3, title:'Types that do not lie: STRICT tables', kind:'Core skill', minutes:14, dialect:'sqlite',
  summary:'STRICT turns the declared type into a rule; the rule accepts six type names, and NUMERIC, DATE and BOOLEAN are not among them.',
  objectives:['Name the six types a STRICT table accepts','Find every column a STRICT table would reject','Say what STRICT still does not enforce'],
  blocks:[
    {type:'syntax', sql:"CREATE TABLE products_strict (\n    id       INTEGER PRIMARY KEY,\n    name     TEXT    NOT NULL,\n    category TEXT    NOT NULL,\n    price    REAL    NOT NULL,\n    stock    INTEGER NOT NULL DEFAULT 0,\n    active   INTEGER NOT NULL DEFAULT 1\n) STRICT;", note:'The rewrite the affinity chapter implies: one storage type per column, chosen from the fixed list, and STRICT on the table. price becomes REAL and active becomes INTEGER because NUMERIC and BOOLEAN are not on it.'},
    {type:'example', id:'ex.r3.demo', sql:"SELECT name, type\nFROM pragma_table_info('products')\nWHERE type NOT IN ('INT', 'INTEGER', 'REAL', 'TEXT', 'BLOB', 'ANY')\nORDER BY cid;", note:'Which of this table\u2019s columns a STRICT declaration would reject: two, and the money column is one of them. Run against a real schema the list is usually longer than anyone expected.',
      result:{columns:['name','type'], rows:[['price','NUMERIC'],['active','BOOLEAN']]}},
    {type:'exercise', id:'ex.r3.1',
      prompt:'Find the whole port: list every column in the file whose declared type is not on the STRICT list, as table_name, column_name and declared_type, ordered by table name and then by column position.',
      starter:"SELECT m.name AS table_name, p.name AS column_name, p.type AS declared_type\nFROM sqlite_master m\nJOIN -- the column list of each table\nWHERE m.type = 'table'\n  AND -- p.type is not on the STRICT list\nORDER BY m.name, p.cid;",
      solution:"SELECT m.name AS table_name, p.name AS column_name, p.type AS declared_type\nFROM sqlite_master m\nJOIN pragma_table_info(m.name) p\nWHERE m.type = 'table'\n  AND p.type NOT IN ('INT', 'INTEGER', 'REAL', 'TEXT', 'BLOB', 'ANY')\nORDER BY m.name, p.cid;",
      hints:['<span class="kbd-inline">JOIN pragma_table_info(m.name) p</span> is a correlated table-valued function: one call per row of <span class="kbd-inline">sqlite_master</span>.','The STRICT list is the literal <span class="kbd-inline">IN</span> list from the chapter, and <span class="kbd-inline">p.cid</span> is the column position.'],
      contract:{columns:['table_name','column_name','declared_type'], column_order:'exact', row_order:'exact'},
      result:{columns:['table_name','column_name','declared_type'], rows:[['customers','signup_date','DATE'],['order_items','unit_price','NUMERIC'],['orders','ordered_at','TIMESTAMP'],['products','price','NUMERIC'],['products','active','BOOLEAN']]}}
  ]},

{ id:'r4', n:4, title:'Constraints, foreign keys and ON CONFLICT', kind:'Core skill', minutes:15, dialect:'sqlite',
  summary:'A foreign key is declared, queryable and off by default; ON CONFLICT makes an upsert one statement instead of a race.',
  objectives:['Say when a declared foreign key is actually enforced','Read the declared keys with pragma_foreign_key_list','Write an upsert with ON CONFLICT DO UPDATE'],
  blocks:[
    {type:'syntax', sql:"INSERT INTO dataset_meta (dataset, version)\nVALUES ('commerce', 2)\nON CONFLICT (dataset) DO UPDATE SET version = excluded.version;", note:'One statement where the alternative is a select-then-insert-or-update with a race between the halves. The conflict target names the unique index that decides.'},
    {type:'example', id:'ex.r4.demo', sql:"SELECT \"table\", \"from\", \"to\"\nFROM pragma_foreign_key_list('order_items')\nORDER BY id;", note:'The two keys the schema declares on order_items, read as rows. ORDER BY id because the engine\u2019s own order is not something to rely on.',
      result:{columns:['table','from','to'], rows:[['products','product_id','id'],['orders','order_id','id']]}},
    {type:'exercise', id:'ex.r4.1',
      prompt:'Audit the whole schema: return one row per declared foreign key as child, parent, child_column and parent_column, ordered by child table and then by the key\u2019s position.',
      starter:"SELECT m.name AS child, f.\"table\" AS parent,\n       f.\"from\" AS child_column, f.\"to\" AS parent_column\nFROM sqlite_master m\nJOIN -- the key list of each table\nWHERE m.type = 'table'\nORDER BY m.name, f.id;",
      solution:"SELECT m.name AS child, f.\"table\" AS parent,\n       f.\"from\" AS child_column, f.\"to\" AS parent_column\nFROM sqlite_master m\nJOIN pragma_foreign_key_list(m.name) f\nWHERE m.type = 'table'\nORDER BY m.name, f.id;",
      hints:['<span class="kbd-inline">pragma_foreign_key_list()</span> is table-valued, exactly like <span class="kbd-inline">pragma_table_info()</span>.','<span class="kbd-inline">"table"</span>, <span class="kbd-inline">"from"</span> and <span class="kbd-inline">"to"</span> are reserved words, so the pragma\u2019s columns are quoted wherever they appear.'],
      contract:{columns:['child','parent','child_column','parent_column'], column_order:'exact', row_order:'exact'},
      result:{columns:['child','parent','child_column','parent_column'], rows:[['order_items','products','product_id','id'],['order_items','orders','order_id','id'],['orders','customers','customer_id','id']]}}
  ]},

{ id:'r5', n:5, title:'JSON, generated columns and expression indexes', kind:'Core skill', minutes:15, dialect:'sqlite',
  summary:'json1 is compiled in, -> keeps JSON while ->> unwraps it, and a generated column can fix the money scale the affinity system will not.',
  objectives:['Build a JSON array from a result set deterministically','Choose between -> and ->> for a comparison','Say when a STORED generated column can be indexed'],
  blocks:[
    {type:'syntax', sql:"SELECT json_extract('{\"sku\": \"QC-1\", \"qty\": 3}', '$.qty') AS qty,\n       '{\"sku\": \"QC-1\"}' ->> '$.sku' AS sku;", note:'No CREATE EXTENSION and no jsonb: a document is TEXT and every function parses it on call. -> returns a JSON value, ->> returns the SQL scalar a WHERE clause wants.'},
    {type:'example', id:'ex.r5.demo', sql:"SELECT category,\n       json_group_array(name) AS products\nFROM (SELECT category, name FROM products ORDER BY category, name)\nGROUP BY category\nORDER BY category;", note:'The inner ORDER BY is load-bearing: the aggregate collects rows in scan order, so ordering the subquery is what makes the array deterministic.',
      result:{columns:['category','products'], rows:[['Electronics','["Aurora Wireless Earbuds","Nimbus Headphones","Noise-Cancel Earmuffs","Pulse Fitness Tracker","Vertex Mechanical Keyboard"]'],['Home','["Bamboo Shelf","Cedar Desk Lamp","Ceramic Vase","Linen Duvet Cover","Wool Throw Blanket"]'],['Kitchen','["Copper Kettle","Espresso Tamper","Granite Cookware Set","Maple Cutting Board"]'],['Outdoor','["Discontinued Camp Stove","Harbor Rain Shell","Rope Dog Leash","Solar Camp Lantern","Summit Hiking Poles","Trail Runner Jacket"]']]}},
    {type:'exercise', id:'ex.r5.1',
      prompt:'For each category, build the JSON array of its product names as products and pull the first element back out as a SQL value with ->>, calling it first_product, ordered by category.',
      starter:"SELECT category,\n       json_group_array(name) AS products,\n       -- the first element, as SQL text\nFROM (SELECT category, name FROM products ORDER BY category, name)\nGROUP BY category\nORDER BY category;",
      solution:"SELECT category,\n       json_group_array(name) AS products,\n       json_group_array(name) ->> '$[0]' AS first_product\nFROM (SELECT category, name FROM products ORDER BY category, name)\nGROUP BY category\nORDER BY category;",
      hints:['The path for the first element of an array is <span class="kbd-inline">\'$[0]\'</span>.','Use <span class="kbd-inline">->></span> (two characters), not <span class="kbd-inline">-></span>: the second returns the element as JSON, the first as a SQL value.'],
      contract:{columns:['category','products','first_product'], column_order:'exact', row_order:'exact'},
      result:{columns:['category','products','first_product'], rows:[['Electronics','["Aurora Wireless Earbuds","Nimbus Headphones","Noise-Cancel Earmuffs","Pulse Fitness Tracker","Vertex Mechanical Keyboard"]','Aurora Wireless Earbuds'],['Home','["Bamboo Shelf","Cedar Desk Lamp","Ceramic Vase","Linen Duvet Cover","Wool Throw Blanket"]','Bamboo Shelf'],['Kitchen','["Copper Kettle","Espresso Tamper","Granite Cookware Set","Maple Cutting Board"]','Copper Kettle'],['Outdoor','["Discontinued Camp Stove","Harbor Rain Shell","Rope Dog Leash","Solar Camp Lantern","Summit Hiking Poles","Trail Runner Jacket"]','Discontinued Camp Stove']]}}
  ]},

{ id:'r6', n:6, title:'Changing a schema with user_version', kind:'Core skill', minutes:14, dialect:'sqlite',
  summary:'Four ALTER TABLE verbs, a documented twelve-step rebuild for everything else, and a version counter the engine stores but never interprets.',
  objectives:['List the ALTER TABLE operations SQLite supports directly','Explain why PRAGMA foreign_keys has to change outside the transaction','Read user_version and say who interprets it'],
  blocks:[
    {type:'syntax', sql:'PRAGMA user_version;', note:'A four-byte integer in the file header, written atomically with the rest of the transaction. The engine stores it and stops there: the application decides what the numbers mean.'},
    {type:'example', id:'ex.r6.demo', sql:"SELECT (SELECT user_version FROM pragma_user_version())      AS user_version,\n       (SELECT application_id FROM pragma_application_id())   AS application_id;", note:'The two header fields in one row. Zero and zero: this fixture has never been migrated and does not claim a file format of its own.',
      result:{columns:['user_version','application_id'], rows:[[0,0]]}},
    {type:'exercise', id:'ex.r6.1',
      prompt:'Compare the header counter with the dataset\u2019s own metadata: return the file\u2019s user_version as app_user_version and the version stored in dataset_meta for the \'commerce\' dataset as dataset_version.',
      starter:"SELECT (SELECT -- the header counter\n       ) AS app_user_version,\n       (SELECT -- the dataset\u2019s own version\n       ) AS dataset_version;",
      solution:"SELECT (SELECT user_version FROM pragma_user_version()) AS app_user_version,\n       (SELECT version FROM dataset_meta WHERE dataset = 'commerce') AS dataset_version;",
      hints:['Both are scalar subqueries: one against a table-valued pragma, one against a real table.','<span class="kbd-inline">dataset_meta</span> has one row per dataset, keyed by <span class="kbd-inline">dataset</span>.'],
      contract:{columns:['app_user_version','dataset_version'], column_order:'exact', row_order:'ignored'},
      result:{columns:['app_user_version','dataset_version'], rows:[[0,1]]}}
  ]},

{ id:'r7', n:7, title:'Backup, ATTACH and shipping the file', kind:'Orientation', minutes:12, dialect:'sqlite',
  summary:'VACUUM INTO and the backup API copy a consistent file, integrity_check proves it, and ATTACH is a second database rather than a schema.',
  objectives:['Say why cp is not a backup while a writer runs','Verify a file with integrity_check and size it in pages','Explain what ATTACH adds and what it does not'],
  blocks:[
    {type:'syntax', sql:'PRAGMA integrity_check;', note:'The verification step for a shipped file: pages, B-tree invariants and the freelist all agree. A backup you cannot integrity-check is a backup you have not tested.'},
    {type:'example', id:'ex.r7.demo', sql:"SELECT (SELECT integrity_check FROM pragma_integrity_check()) AS integrity,\n       (SELECT page_size  FROM pragma_page_size())             AS page_size,\n       (SELECT page_count FROM pragma_page_count())            AS pages;", note:'The whole commerce dataset in eleven 4 KiB pages, and the file checks out.',
      result:{columns:['integrity','page_size','pages'], rows:[['ok',4096,11]]}},
    {type:'exercise', id:'ex.r7.1',
      prompt:'Size the file the way a copy operation would: return page_size, pages and the total size in kibibytes as kib, in one row.',
      starter:"SELECT (SELECT page_size FROM pragma_page_size()) AS page_size,\n       -- how many pages\n       -- total size in kibibytes\n;",
      solution:"SELECT (SELECT page_size FROM pragma_page_size()) AS page_size,\n       (SELECT page_count FROM pragma_page_count()) AS pages,\n       (SELECT page_size FROM pragma_page_size()) * (SELECT page_count FROM pragma_page_count()) / 1024 AS kib;",
      hints:['Bytes are <span class="kbd-inline">page_size * page_count</span>; divide by 1024 for KiB.','Integer division is exact here: the product is a whole multiple of 1024.'],
      contract:{columns:['page_size','pages','kib'], column_order:'exact', row_order:'ignored'},
      result:{columns:['page_size','pages','kib'], rows:[[4096,11,44]]}}
  ]}
];


var POSTGRES_TO_SQLITE_MODULES: CourseModule[] = [
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

/* `dataset` names the workspace a course's SQL runs against, the same way
   `dialect` names the engine, and for the same reason: the perf course states
   numbers ("customer 7 has 184 orders", "10 000 orders, 20 000 order items")
   that only exist in the perf fixture, so every cell in the course has to run
   there. It defaults to `commerce`; `web/src/lib/dataset.ts` is the only
   reader, and scripts/content-audit.mjs fails if a course names a dataset the
   app cannot serve or an exercise carries a perf contract outside one. */
/* Every course authored before M6 teaches PostgreSQL, and each says so once
   here rather than on each of its chapters (design §15 D15): a chapter that
   teaches another engine overrides it. The value is machine-checked against the
   closed dialect set — `web/src/lib/dialect.ts` is the only reader, and
   `scripts/content-audit.mjs` fails if a course or chapter ever carries a
   dialect the app cannot run. */
var COURSES: Course[] = [
  { id:'sql-analytics', title:'SQL Analytics in the Real World', domain:'Commerce & retail', level:'Intermediate', minutes:120,
    source:'created', enrolled:true, built:true, dialect:'postgresql', modules:MODULES,
    blurb:'Go beyond basic aggregation: revenue reports, ranking, cohort retention and query reasoning on one realistic commerce dataset.' },
  { id:'joins', title:'Joins without guesswork', domain:'Commerce & retail', level:'Beginner → intermediate', minutes:75,
    source:'library', enrolled:true, built:true, dialect:'postgresql', modules:JOINS_MODULES,
    blurb:'Understand join behaviour through data: unmatched rows, duplicate keys, NULLs and the grain trap that inflates every total.' },
  { id:'plans', title:'PostgreSQL query plans', domain:'Query tuning', level:'Intermediate', minutes:90,
    source:'library', enrolled:true, built:true, dialect:'postgresql', dataset:'perf', modules:PLANS_MODULES,
    blurb:'Read EXPLAIN ANALYZE, understand index scans and selectivity, and reason about joins and row estimates.' },
  { id:'subqueries', title:'Subqueries and set operations', domain:'Commerce & retail', level:'Intermediate', minutes:70,
    source:'library', enrolled:true, built:true, dialect:'postgresql', modules:SUBQUERIES_MODULES,
    blurb:'Use one query as the input to another: scalar and list subqueries, correlated EXISTS, the NOT IN NULL trap, and set operations that compare whole result sets.' },
  { id:'foundations', title:'SQL Foundations', domain:'Commerce & retail', level:'Beginner', minutes:100,
    source:'library', enrolled:true, built:true, dialect:'postgresql', modules:FOUNDATIONS_MODULES,
    blurb:'The clauses underneath every query: projection and DISTINCT, WHERE and boolean precedence, ORDER BY with LIMIT and OFFSET, NULL, IN/BETWEEN/LIKE, CASE, and aggregation with HAVING.' },
  { id:'sqlite-essentials', title:'SQLite Essentials', domain:'SQLite', level:'Beginner \u2192 intermediate', minutes:99,
    source:'library', enrolled:true, built:true, dialect:'sqlite', modules:SQLITE_ESSENTIALS_MODULES,
    blurb:'The engine on its own terms: a database that is a file, storage classes and type affinity, the rowid, dates as ISO-8601 text, LIKE and GLOB, reading EXPLAIN QUERY PLAN, and an honest account of what SQLite cannot do.' },
  { id:'sqlite-in-production', title:'SQLite in Production', domain:'SQLite', level:'Intermediate \u2192 advanced', minutes:100,
    source:'library', enrolled:true, built:true, dialect:'sqlite', modules:SQLITE_IN_PRODUCTION_MODULES,
    blurb:'The parts of SQLite that surprise application developers: one writer and WAL, the pragmas that are per-connection, STRICT tables, foreign keys that do not fire by default, JSON and generated columns, schema changes with user_version, and shipping the file.' },
  { id:'postgres-to-sqlite', title:'Postgres to SQLite', domain:'SQLite', level:'Intermediate \u2192 advanced', minutes:100,
    source:'library', enrolled:true, built:true, dialect:'sqlite', modules:POSTGRES_TO_SQLITE_MODULES,
    blurb:'A translation course: :: becomes CAST, DATE_TRUNC becomes strftime, DISTINCT ON becomes a window, INTERVAL becomes date modifiers, numeric becomes REAL, serial becomes the rowid — plus the short honest list of PostgreSQL constructs that have no SQLite port at all.' }
];


export { TABLES, MODULES, JOINS_MODULES, SUBQUERIES_MODULES, FOUNDATIONS_MODULES, PLANS_MODULES, COURSES };
export { SQLITE_ESSENTIALS_MODULES, SQLITE_IN_PRODUCTION_MODULES, POSTGRES_TO_SQLITE_MODULES };
