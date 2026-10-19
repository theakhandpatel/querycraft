import type { CourseModule } from '../../types';
import { intro } from '../courseIntro';

export const SUBQUERIES_MODULES: CourseModule[] = [
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
