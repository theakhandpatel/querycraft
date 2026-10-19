import type { CourseModule } from '../../types';

export const SQLITE_ESSENTIALS_MODULES: CourseModule[] = [
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
