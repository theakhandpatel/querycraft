import type { CourseModule } from '../../types';
import { intro } from '../courseIntro';

export const JOINS_MODULES: CourseModule[] = [
{ id:'j1', n:1, title:'What a join actually does', kind:'Core skill', minutes:14,
  summary:'Match rows between two tables with ON, and see exactly which rows survive.',
  objectives:['Join a child table to its parent','Understand what ON matches','Keep the result at the grain you asked for'],
  blocks:[
    intro('A join walks two tables and pairs rows where a condition holds. <span class="kbd-inline">orders.customer_id</span> equals <span class="kbd-inline">customers.id</span>, so an inner join keeps only orders whose customer exists — and only customers who have orders.'),
    intro('Read the join as a sentence: “for every order, find the customer it belongs to.” The <span class="kbd-inline">ON</span> clause is the matching rule; everything else you already know.'),
    {type:'syntax', sql:'SELECT o.id AS order_id, c.name AS customer\nFROM orders o\nJOIN customers c ON c.id = o.customer_id\nORDER BY o.id;', note:'JOIN (inner) keeps rows that match on both sides.'},
    {type:'example', id:'ex.j1.demo', sql:"SELECT o.id AS order_id, c.name AS customer\nFROM orders o\nJOIN customers c ON c.id = o.customer_id\nORDER BY o.id\nLIMIT 4;", note:'One row per order, enriched with the customer name.',
      result:{columns:['order_id','customer'], rows:[['1001','Ada Whitfield'],['1002','Ada Whitfield'],['1003','Bruno Sala'],['1004','Chidi Okafor']]}},
    {type:'exercise', id:'ex.j1.1',
      prompt:'Return each order id, its customer name and its status, newest order first.',
      starter:"SELECT o.id AS order_id,\n       c.name AS customer_name,\n       -- status\nFROM orders o\n-- join customers\n-- newest first",
      solution:"SELECT o.id AS order_id,\n       c.name AS customer_name,\n       o.status\nFROM orders o\nJOIN customers c ON c.id = o.customer_id\nORDER BY o.ordered_at DESC;",
      hints:['Join <span class="kbd-inline">customers c ON c.id = o.customer_id</span>.','Order by <span class="kbd-inline">o.ordered_at DESC</span> for newest first.'],
      contract:{columns:['order_id','customer_name','status'], column_order:'exact', row_order:'ignored'},
      result:{columns:['order_id','customer_name','status'],rows:[['1080','Mina Rossi','completed'],['1079','Luca Novak','completed'],['1078','Kira Whitfield','completed'],['1077','Jonas Larsen','refunded'],['1076','Ines Haidar','shipped'],['1075','Hiro Ivanov','cancelled'],['1074','Gina Rahman','pending'],['1073','Farid Lindqvist','completed'],['1072','Emi Tanaka','completed'],['1071','Xiu Berg','completed'],['1070','Wren Rossi','refunded'],['1069','Viktor Novak','shipped'],['1068','Uma Whitfield','cancelled'],['1067','Tess Larsen','pending'],['1066','Sami Haidar','completed'],['1065','Rosa Ivanov','completed'],['1064','Quinn Rahman','completed'],['1063','Pia Lindqvist','refunded'],['1062','Omar Park','shipped'],['1061','Noor Berg','cancelled'],['1060','Mina Rossi','pending'],['1059','Luca Novak','completed'],['1058','Kira Whitfield','completed'],['1057','Jonas Larsen','completed'],['1056','Ines Haidar','refunded'],['1055','Hiro Ivanov','shipped'],['1054','Gina Rahman','cancelled'],['1053','Farid Lindqvist','pending'],['1052','Emi Tanaka','completed'],['1051','Xiu Berg','completed'],['1050','Wren Rossi','completed'],['1049','Viktor Novak','refunded'],['1048','Uma Whitfield','shipped'],['1047','Tess Larsen','cancelled'],['1046','Sami Haidar','pending'],['1045','Rosa Ivanov','completed'],['1044','Quinn Rahman','completed'],['1043','Pia Lindqvist','completed'],['1042','Omar Park','refunded'],['1041','Noor Berg','shipped'],['1040','Mina Rossi','cancelled'],['1039','Luca Novak','pending'],['1038','Kira Whitfield','completed'],['1037','Jonas Larsen','completed'],['1036','Ines Haidar','completed'],['1035','Hiro Ivanov','refunded'],['1034','Gina Rahman','shipped'],['1033','Farid Lindqvist','cancelled'],['1032','Emi Tanaka','pending'],['1031','Xiu Berg','completed'],['1030','Wren Rossi','completed'],['1029','Viktor Novak','completed'],['1028','Uma Whitfield','refunded'],['1027','Tess Larsen','shipped'],['1026','Sami Haidar','cancelled'],['1005','Ada Whitfield','completed'],['1025','Rosa Ivanov','pending'],['1024','Quinn Rahman','completed'],['1023','Pia Lindqvist','completed'],['1022','Omar Park','completed'],['1021','Noor Berg','refunded'],['1020','Mina Rossi','shipped'],['1004','Chidi Okafor','completed'],['1019','Luca Novak','cancelled'],['1018','Kira Whitfield','pending'],['1017','Jonas Larsen','completed'],['1016','Ines Haidar','completed'],['1015','Hiro Ivanov','completed'],['1014','Gina Rahman','refunded'],['1003','Bruno Sala','cancelled'],['1013','Farid Lindqvist','shipped'],['1012','Emi Tanaka','cancelled'],['1002','Ada Whitfield','completed'],['1011','Chidi Okafor','pending'],['1010','Chidi Okafor','completed'],['1009','Chidi Okafor','completed'],['1008','Bruno Sala','completed'],['1007','Ada Whitfield','refunded'],['1006','Ada Whitfield','shipped'],['1001','Ada Whitfield','completed']]}},
  ]},

{ id:'j2', n:2, title:'Left joins and the rows that disappear', kind:'Core skill', minutes:16,
  summary:'Use LEFT JOIN to keep every row on one side — including customers with no orders.',
  objectives:['Keep unmatched rows with LEFT JOIN','Count related rows without losing zeros','Spot an inner join hiding missing data'],
  blocks:[
    intro('An inner <span class="kbd-inline">JOIN</span> is silently a filter: rows with no match vanish. A <span class="kbd-inline">LEFT JOIN</span> keeps every row from the left table and fills the right side with <span class="kbd-inline">NULL</span> when nothing matches.'),
    intro('Because unmatched rows appear with <span class="kbd-inline">NULL</span> on the right, <span class="kbd-inline">COUNT(o.id)</span> counts matches and returns 0, while <span class="kbd-inline">COUNT(*)</span> would wrongly count the customer row itself.'),
    {type:'syntax', sql:'SELECT c.name, COUNT(o.id) AS orders\nFROM customers c\nLEFT JOIN orders o ON o.customer_id = c.id\nGROUP BY c.id, c.name\nORDER BY orders DESC;', note:'LEFT JOIN keeps customers even with zero orders.'},
    {type:'example', id:'ex.j2.demo', sql:"SELECT c.name, COUNT(o.id) AS orders\nFROM customers c\nLEFT JOIN orders o ON o.customer_id = c.id\nGROUP BY c.id, c.name\nORDER BY orders ASC, c.id\nLIMIT 4;", note:'Customers with no orders show a count of 0.',
      result:{columns:['name','orders'], rows:[['Dara Novak','0'],['Yara Park','0'],['Zane Lindqvist','0'],['Aya Rahman','0']]}},
    {type:'exercise', id:'ex.j2.1',
      prompt:'Return every customer name and how many orders they placed, including customers with none. Highest count first.',
      starter:"SELECT c.name, COUNT(o.id) AS orders\nFROM customers c\n-- left join orders\n-- group by customer\n-- highest first",
      solution:"SELECT c.name, COUNT(o.id) AS orders\nFROM customers c\nLEFT JOIN orders o ON o.customer_id = c.id\nGROUP BY c.id, c.name\nORDER BY orders DESC, c.name;",
      hints:['Use <span class="kbd-inline">LEFT JOIN</span>, not JOIN.','Count <span class="kbd-inline">o.id</span> so unmatched rows count as 0.'],
      contract:{columns:['name','orders'], column_order:'exact', row_order:'ignored'},
      result:{columns:['name','orders'],rows:[['Ada Whitfield','5'],['Chidi Okafor','4'],['Emi Tanaka','4'],['Farid Lindqvist','4'],['Gina Rahman','4'],['Hiro Ivanov','4'],['Ines Haidar','4'],['Jonas Larsen','4'],['Kira Whitfield','4'],['Luca Novak','4'],['Mina Rossi','4'],['Noor Berg','3'],['Omar Park','3'],['Pia Lindqvist','3'],['Quinn Rahman','3'],['Rosa Ivanov','3'],['Sami Haidar','3'],['Tess Larsen','3'],['Uma Whitfield','3'],['Viktor Novak','3'],['Wren Rossi','3'],['Xiu Berg','3'],['Bruno Sala','2'],['Aya Rahman','0'],['Bo Ivanov','0'],['Cleo Haidar','0'],['Dara Novak','0'],['Dev Larsen','0'],['Yara Park','0'],['Zane Lindqvist','0']]}},
  ]},

{ id:'j3', n:3, title:'The grain trap: one-to-many fan-out', kind:'Advanced', minutes:18,
  summary:'Joining a parent to its children multiplies rows — count the right thing.',
  objectives:['See why joins fan rows out','Count distinct keys after a fan-out','Aggregate at the grain the question wants'],
  blocks:[
    intro('One order has many line items. Join them and each order row multiplies into one row per line. That is correct when you want lines and wrong when you want orders — and <span class="kbd-inline">COUNT(*)</span> after the join counts lines, not orders.'),
    intro('The fix is to name the grain: <span class="kbd-inline">COUNT(DISTINCT o.id)</span> counts orders even after the fan-out. When two numbers disagree, the grain is usually why.'),
    {type:'syntax', sql:'SELECT o.id, COUNT(oi.id) AS lines\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nGROUP BY o.id;', note:'GROUP BY brings the fan-out back to one row per order.'},
    {type:'example', id:'ex.j3.demo', sql:"SELECT o.id AS order_id, COUNT(oi.id) AS lines\nFROM orders o\nJOIN order_items oi ON oi.order_id = o.id\nGROUP BY o.id\nORDER BY lines DESC, o.id\nLIMIT 4;", note:'How many lines each order contains.',
      result:{columns:['order_id','lines'], rows:[['1006','4'],['1009','4'],['1008','3'],['1012','3']]}},
    {type:'exercise', id:'ex.j3.1',
      prompt:'Return each product name and how many distinct orders it appears in, most first. Count orders, not line items.',
      starter:"SELECT p.name, COUNT(DISTINCT oi.order_id) AS orders\nFROM products p\n-- join order_items\n-- group by product\n-- most first",
      solution:"SELECT p.name, COUNT(DISTINCT oi.order_id) AS orders\nFROM products p\nJOIN order_items oi ON oi.product_id = p.id\nGROUP BY p.id, p.name\nORDER BY orders DESC, p.name;",
      hints:['Join <span class="kbd-inline">order_items oi ON oi.product_id = p.id</span>.','Use <span class="kbd-inline">COUNT(DISTINCT oi.order_id)</span> to avoid counting lines twice.'],
      contract:{columns:['name','orders'], column_order:'exact', row_order:'ignored'},
      result:{columns:['name','orders'],rows:[['Aurora Wireless Earbuds','10'],['Trail Runner Jacket','10'],['Granite Cookware Set','9'],['Linen Duvet Cover','9'],['Nimbus Headphones','9'],['Rope Dog Leash','9'],['Bamboo Shelf','8'],['Cedar Desk Lamp','8'],['Ceramic Vase','8'],['Copper Kettle','8'],['Espresso Tamper','8'],['Harbor Rain Shell','8'],['Maple Cutting Board','8'],['Noise-Cancel Earmuffs','8'],['Pulse Fitness Tracker','8'],['Solar Camp Lantern','8'],['Summit Hiking Poles','8'],['Vertex Mechanical Keyboard','8'],['Wool Throw Blanket','8']]}}
  ]}
];
