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
      result:{columns:['id','name','city'], rows:[['1','Ada Whitfield','Leeds'],['2','Bruno Sala','Milan'],['3','Chidi Okafor',null],['4','Dara Novak',null],['5','Emi Tanaka','Osaka']]}},
    {type:'exercise', id:'ex.1.1',
      prompt:'Return the name, category and price of every product in the Electronics category, cheapest first.',
      starter:"SELECT\n    -- name, category, price\nFROM products\n-- filter to Electronics\n-- order by price",
      solution:"SELECT name, category, price\nFROM products\nWHERE category = 'Electronics'\nORDER BY price;",
      hints:['Filter with <span class="kbd-inline">WHERE category = \'Electronics\'</span>.','Add <span class="kbd-inline">ORDER BY price</span> (ascending is the default).'],
      contract:{columns:['name','category','price'], column_order:'exact', row_order:'exact'},
      checks:[
        {label:'Projects name, category, price', code:'COLUMN_MISMATCH', test:function(s){ return /select[\s\S]*\bname\b[\s\S]*\bcategory\b[\s\S]*\bprice\b/i.test(s); }},
        {label:'Filters to the Electronics category', code:'FILTER_MISSING', test:function(s){ return /category\s*=\s*'electronics'/i.test(s); }},
        {label:'Orders by price ascending', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by\s+price\b/i.test(s) && !/price\s+desc/i.test(s); }}
      ],
      result:{columns:['name','category','price'], rows:[['Aurora Wireless Earbuds','Electronics','59.00'],['Pulse Fitness Tracker','Electronics','74.25'],['Vertex Mechanical Keyboard','Electronics','89.50'],['Nimbus Headphones','Electronics','129.00'],['Lumen 27-inch 4K Monitor','Electronics','329.00']]}}
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
      checks:[
        {label:'Joins customers and order_items', code:'JOIN_MISSING', test:function(s){ return /join\s+customers/i.test(s) && /join\s+order_items/i.test(s); }},
        {label:'Counts line items', code:'AGGREGATION_MISSING', test:function(s){ return /count\s*\(/i.test(s); }},
        {label:'Groups before aggregating', code:'GROUPING_MISSING', test:function(s){ return /group\s+by/i.test(s); }},
        {label:'Orders by line count descending', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*desc/i.test(s); }}
      ],
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
      checks:[
        {label:'Computes revenue from quantity and unit price', code:'VALUE_MISMATCH', test:function(s){ return /quantity\s*\*\s*unit_price|unit_price\s*\*\s*quantity/i.test(s); }},
        {label:'Filters to completed orders', code:'FILTER_MISSING', test:function(s){ return /status\s*=\s*'completed'/i.test(s); }},
        {label:'Groups by month', code:'GROUPING_MISSING', test:function(s){ return /date_trunc\s*\(\s*'month'/i.test(s) && /group\s+by/i.test(s); }},
        {label:'Orders months ascending', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*1\b|order\s+by[\s\S]*month/i.test(s); }}
      ],
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
      checks:[
        {label:'Uses a WITH clause', code:'CTE_MISSING', test:function(s){ return /^\s*with\b/i.test(s); }},
        {label:'Counts per customer', code:'AGGREGATION_MISSING', test:function(s){ return /count\s*\(\s*\*\s*\)/i.test(s) && /group\s+by\s+customer_id/i.test(s); }},
        {label:'Keeps customers with 3 or more', code:'FILTER_MISSING', test:function(s){ return /(>=)\s*3|>\s*2\b/.test(s); }},
        {label:'Orders by count descending', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*desc/i.test(s); }}
      ],
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
      checks:[
        {label:'Uses a window function with OVER', code:'WINDOW_MISSING', test:function(s){ return /over\s*\(/i.test(s); }},
        {label:'Partitions by category', code:'PARTITION_MISSING', test:function(s){ return /partition\s+by\s+category/i.test(s); }},
        {label:'Orders the window by price descending', code:'WINDOW_ORDER_MISSING', test:function(s){ return /order\s+by\s+price\s+desc/i.test(s); }},
        {label:'Uses RANK for ties', code:'RANK_FUNCTION_MISMATCH', test:function(s){ return /\brank\s*\(\s*\)/i.test(s); }},
        {label:'Does not collapse rows with GROUP BY', code:'GRAIN_ERROR', test:function(s){ return !/group\s+by/i.test(s); }}
      ],
      result:{columns:['name','category','price','price_rank'], rows:[['Lumen 27-inch 4K Monitor','Electronics','329.00','1'],['Nimbus Headphones','Electronics','129.00','2'],['Vertex Mechanical Keyboard','Electronics','89.50','3'],['Aurora Wireless Earbuds','Electronics','59.00','4'],['Oak Bookshelf','Home','210.00','1']]}},
    intro('Now the real pattern: aggregate first, then rank the aggregate. Ranking individual line items would rank the wrong thing. And when months have no qualifying rows, decide whether the month should appear at all \u2014 that is the contract, and it is decided by the exercise author, not the engine.', 'Aggregate first, then rank'),
    {type:'exercise', id:'ex.5.2',
      prompt:'For each month, return the two product categories with the highest net revenue from completed orders. Return month, category, net_revenue, and rank. Highest revenue first within each month, months oldest first.',
      starter:"WITH monthly AS (\n    SELECT DATE_TRUNC('month', o.ordered_at)::date AS month,\n           p.category,\n           SUM(oi.quantity * oi.unit_price) AS net_revenue\n    FROM orders o\n    JOIN order_items oi ON oi.order_id = o.id\n    JOIN products p ON p.id = oi.product_id\n    -- completed only\n    -- group by month, category\n)\nSELECT * FROM monthly\n-- rank within month, keep top 2",
      solution:"WITH monthly AS (\n    SELECT DATE_TRUNC('month', o.ordered_at)::date AS month,\n           p.category,\n           SUM(oi.quantity * oi.unit_price) AS net_revenue\n    FROM orders o\n    JOIN order_items oi ON oi.order_id = o.id\n    JOIN products p ON p.id = oi.product_id\n    WHERE o.status = 'completed'\n    GROUP BY 1, 2\n),\nranked AS (\n    SELECT month, category, net_revenue,\n           RANK() OVER (\n             PARTITION BY month\n             ORDER BY net_revenue DESC\n           ) AS rank\n    FROM monthly\n)\nSELECT month, category, net_revenue, rank\nFROM ranked\nWHERE rank <= 2\nORDER BY month, rank, category;",
      hints:['Aggregate to one row per month and category in the CTE first.','Apply <span class="kbd-inline">RANK() OVER (PARTITION BY month ORDER BY net_revenue DESC)</span> to the aggregate, then filter <span class="kbd-inline">rank &lt;= 2</span>.'],
      contract:{columns:['month','category','net_revenue','rank'], column_order:'exact', row_order:'exact'},
      checks:[
        {label:'Aggregates before ranking', code:'GRAIN_ERROR', test:function(s){ return /group\s+by/i.test(s) && /over\s*\(/i.test(s); }},
        {label:'Partitions the rank by month', code:'PARTITION_MISSING', test:function(s){ return /partition\s+by\s+month/i.test(s); }},
        {label:'Filters to top two', code:'FILTER_MISSING', test:function(s){ return /rank\s*<=\s*2/i.test(s); }},
        {label:'Keeps only completed orders', code:'FILTER_MISSING', test:function(s){ return /status\s*=\s*'completed'/i.test(s); }}
      ],
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
      checks:[
        {label:'Computes a running count per customer', code:'WINDOW_MISSING', test:function(s){ return /count\s*\(\s*\*\s*\)\s*over/i.test(s) && /partition\s+by\s+customer_id/i.test(s); }},
        {label:'Filters repeat status at 2 or more', code:'FILTER_MISSING', test:function(s){ return /orders_so_far\s*>=\s*2/i.test(s); }},
        {label:'Counts distinct customers per month', code:'AGGREGATION_MISSING', test:function(s){ return /count\s*\(\s*distinct\s+customer_id/i.test(s); }},
        {label:'Only completed orders', code:'FILTER_MISSING', test:function(s){ return /status\s*=\s*'completed'/i.test(s); }}
      ],
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
      checks:[
        {label:'Groups completed orders by month', code:'AGGREGATION_MISSING', test:function(s){ return /status\s*=\s*'completed'/i.test(s) && /group\s+by/i.test(s) && /date_trunc/i.test(s); }},
        {label:'Explains the access method', code:'EXPLANATION_MISSING', test:function(s){ return /scan|index|selectiv/i.test(s); }}
      ],
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
      checks:[
        {label:'Uses a multi-step CTE chain', code:'CTE_MISSING', test:function(s){ return /^\s*with\b/i.test(s) && /group\s+by/i.test(s); }},
        {label:'Counts distinct buyers', code:'AGGREGATION_MISSING', test:function(s){ return /count\s*\(\s*distinct\s+customer_id/i.test(s); }},
        {label:'Ranks within month with a tie-break', code:'WINDOW_ORDER_MISSING', test:function(s){ return /partition\s+by\s+month/i.test(s) && /order\s+by[\s\S]*category/i.test(s); }},
        {label:'Keeps one row per month', code:'FILTER_MISSING', test:function(s){ return /rn\s*=\s*1|row_number|rank\s*=\s*1/i.test(s); }},
        {label:'Only completed orders', code:'FILTER_MISSING', test:function(s){ return /status\s*=\s*'completed'/i.test(s); }}
      ],
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
      checks:[
        {label:'Uses a recursive CTE', code:'CTE_MISSING', test:function(s){ return /with\s+recursive/i.test(s); }},
        {label:'Stops the recursion in June', code:'RECURSION_MISSING', test:function(s){ return /month\s*<\s*date\s*'2026-06-01'/i.test(s); }},
        {label:'Keeps every month from the series', code:'JOIN_MISSING', test:function(s){ return /left\s+join/i.test(s); }},
        {label:'Keeps only completed orders', code:'FILTER_MISSING', test:function(s){ return /status\s*=\s*'completed'/i.test(s); }},
        {label:'Counts orders, not series rows', code:'AGGREGATION_MISSING', test:function(s){ return /count\s*\(\s*o\.id\s*\)/i.test(s); }}
      ],
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
      checks:[
        {label:'Joins customers to orders', code:'JOIN_MISSING', test:function(s){ return /join\s+customers/i.test(s); }},
        {label:'Returns the order status', code:'COLUMN_MISMATCH', test:function(s){ return /\bstatus\b/i.test(s); }},
        {label:'Orders newest first', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*desc/i.test(s); }}
      ],
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
      checks:[
        {label:'Uses a LEFT JOIN', code:'JOIN_TYPE_MISMATCH', test:function(s){ return /left\s+join/i.test(s); }},
        {label:'Counts joined rows, not star', code:'AGGREGATION_MISSING', test:function(s){ return /count\s*\(\s*o\.id\s*\)/i.test(s); }},
        {label:'Groups by customer', code:'GROUPING_MISSING', test:function(s){ return /group\s+by/i.test(s); }}
      ],
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
      checks:[
        {label:'Joins order_items to products', code:'JOIN_MISSING', test:function(s){ return /join\s+order_items/i.test(s); }},
        {label:'Counts distinct orders', code:'GRAIN_ERROR', test:function(s){ return /count\s*\(\s*distinct\s+oi\.order_id/i.test(s) || /count\s*\(\s*distinct\s+order_id/i.test(s); }},
        {label:'Groups by product', code:'GROUPING_MISSING', test:function(s){ return /group\s+by/i.test(s); }}
      ],
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
      checks:[
        {label:'Filters with a subquery', code:'SUBQUERY_MISSING', test:function(s){ return /\(\s*select\b/i.test(s); }},
        {label:'Compares against the average price', code:'AGGREGATION_MISSING', test:function(s){ return /avg\s*\(\s*price/i.test(s); }},
        {label:'Orders most expensive first', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*desc/i.test(s); }}
      ],
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
      checks:[
        {label:'Correlates the subquery with the outer product', code:'SUBQUERY_MISSING', test:function(s){ return /product_id\s*=\s*p\.id/i.test(s); }},
        {label:'Counts lines per product', code:'AGGREGATION_MISSING', test:function(s){ return /count\s*\(/i.test(s); }},
        {label:'Keeps one row per product', code:'GRAIN_ERROR', test:function(s){ return /order\s+by\s+p\.id/i.test(s); }}
      ],
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
      checks:[
        {label:'Uses the NULL-safe anti-join', code:'NULL_SAFETY', test:function(s){ return /not\s+exists/i.test(s) || (/left\s+join/i.test(s) && /is\s+null/i.test(s)); }},
        {label:'Avoids NOT IN', code:'NULL_TRAP', test:function(s){ return !/not\s+in\s*\(/i.test(s); }},
        {label:'Orders by name', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*name/i.test(s); }}
      ],
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
      checks:[
        {label:'Uses a set operation', code:'SET_OP_MISSING', test:function(s){ return /except|intersect|union/i.test(s); }},
        {label:'Subtracts the customers that have orders', code:'JOIN_MISSING', test:function(s){ return /from\s+orders/i.test(s); }},
        {label:'Orders by name', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*name/i.test(s); }}
      ],
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
      checks:[
        {label:'Uses DISTINCT', code:'DISTINCT_MISSING', test:function(s){ return /select\s+distinct/i.test(s); }},
        {label:'Projects category and active', code:'COLUMN_MISMATCH', test:function(s){ return /\bcategory\b/i.test(s) && /\bactive\b/i.test(s); }},
        {label:'Orders by category then active', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*category[\s\S]*active/i.test(s); }}
      ],
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
      checks:[
        {label:'Keeps only active products', code:'FILTER_MISSING', test:function(s){ return /active/i.test(s); }},
        {label:'Excludes the Electronics category', code:'FILTER_MISSING', test:function(s){ return /(not\s+category\s*=|category\s*<>|category\s*!=)/i.test(s); }},
        {label:'Restricts to price at least 90', code:'FILTER_MISSING', test:function(s){ return /price\s*>=\s*90/i.test(s); }},
        {label:'Cheapest first, ties by name', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*price/i.test(s) && /order\s+by[\s\S]*name/i.test(s); }}
      ],
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
      checks:[
        {label:'Sorts by price descending', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by\s+price\s+desc/i.test(s); }},
        {label:'Breaks ties by name', code:'ROW_ORDER_MISMATCH', test:function(s){ return /price\s+desc\s*,\s*name/i.test(s); }},
        {label:'Returns three rows', code:'ROW_COUNT_MISMATCH', test:function(s){ return /limit\s+3/i.test(s); }},
        {label:'Skips the first two pages', code:'ROW_COUNT_MISMATCH', test:function(s){ return /offset\s+6/i.test(s); }}
      ],
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
      checks:[
        {label:'Tests for a missing city with IS NULL', code:'FILTER_MISSING', test:function(s){ return /is\s+null/i.test(s) && !/=\s*null/i.test(s); }},
        {label:'Replaces the missing city with COALESCE', code:'COLUMN_MISMATCH', test:function(s){ return /coalesce/i.test(s); }},
        {label:'Orders by name', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*name/i.test(s); }}
      ],
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
      checks:[
        {label:'Matches categories with IN', code:'FILTER_MISSING', test:function(s){ return /\bin\s*\(/i.test(s); }},
        {label:'Matches the price range with BETWEEN', code:'FILTER_MISSING', test:function(s){ return /\bbetween\s+20\s+and\s+90/i.test(s); }},
        {label:'Cheapest first, ties by name', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*price/i.test(s) && /order\s+by[\s\S]*name/i.test(s); }}
      ],
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
      checks:[
        {label:'Uses a CASE expression', code:'CASE_MISSING', test:function(s){ return /\bcase\b/i.test(s); }},
        {label:'Labels zero stock as out', code:'VALUE_MISMATCH', test:function(s){ return /stock\s*=\s*0/i.test(s); }},
        {label:'Labels under 20 as low', code:'VALUE_MISMATCH', test:function(s){ return /stock\s*<\s*20/i.test(s); }},
        {label:'Provides an ELSE branch', code:'VALUE_MISMATCH', test:function(s){ return /\belse\b/i.test(s); }},
        {label:'Orders by stock then name', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*stock/i.test(s) && /order\s+by[\s\S]*name/i.test(s); }}
      ],
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
      checks:[
        {label:'Groups by status', code:'AGGREGATION_MISSING', test:function(s){ return /group\s+by[\s\S]*status/i.test(s); }},
        {label:'Counts the orders', code:'AGGREGATION_MISSING', test:function(s){ return /count\s*\(/i.test(s); }},
        {label:'Filters the groups with HAVING', code:'FILTER_MISSING', test:function(s){ return /having/i.test(s); }},
        {label:'Most orders first, ties by status', code:'ROW_ORDER_MISMATCH', test:function(s){ return /order\s+by[\s\S]*orders\s+desc/i.test(s) && /orders\s+desc\s*,\s*status/i.test(s); }}
      ],
      result:{columns:['status','orders'], rows:[['completed',37],['cancelled',11],['refunded',11],['shipped',11]]}}
  ]}
];

var COURSES: Course[] = [
  { id:'sql-analytics', title:'SQL Analytics in the Real World', domain:'Commerce & retail', level:'Intermediate', minutes:120,
    source:'created', enrolled:true, built:true, modules:MODULES,
    blurb:'Go beyond basic aggregation: revenue reports, ranking, cohort retention and query reasoning on one realistic commerce dataset.' },
  { id:'joins', title:'Joins without guesswork', domain:'Commerce & retail', level:'Beginner → intermediate', minutes:75,
    source:'library', enrolled:true, built:true, modules:JOINS_MODULES,
    blurb:'Understand join behaviour through data: unmatched rows, duplicate keys, NULLs and the grain trap that inflates every total.' },
  { id:'plans', title:'PostgreSQL query plans', domain:'Query tuning', level:'Intermediate', minutes:90,
    source:'library', enrolled:false, built:false, modules:null,
    blurb:'Read EXPLAIN ANALYZE, understand index scans and selectivity, and reason about joins and row estimates.' },
  { id:'subqueries', title:'Subqueries and set operations', domain:'Commerce & retail', level:'Intermediate', minutes:70,
    source:'library', enrolled:true, built:true, modules:SUBQUERIES_MODULES,
    blurb:'Use one query as the input to another: scalar and list subqueries, correlated EXISTS, the NOT IN NULL trap, and set operations that compare whole result sets.' },
  { id:'foundations', title:'SQL Foundations', domain:'Commerce & retail', level:'Beginner', minutes:100,
    source:'library', enrolled:true, built:true, modules:FOUNDATIONS_MODULES,
    blurb:'The clauses underneath every query: projection and DISTINCT, WHERE and boolean precedence, ORDER BY with LIMIT and OFFSET, NULL, IN/BETWEEN/LIKE, CASE, and aggregation with HAVING.' }
];


export { TABLES, MODULES, JOINS_MODULES, SUBQUERIES_MODULES, FOUNDATIONS_MODULES, COURSES };
