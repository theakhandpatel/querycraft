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
