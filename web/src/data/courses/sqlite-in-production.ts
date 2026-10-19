import type { CourseModule } from '../../types';

export const SQLITE_IN_PRODUCTION_MODULES: CourseModule[] = [
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
