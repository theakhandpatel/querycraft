import type { TableDef } from '../types';

export const TABLES: TableDef[] = [
  {name:'customers', rows:30, cols:[['id','integer','PK'],['name','text',''],['email','text','nullable'],['city','text','nullable'],['signup_date','date','']],
   preview:[['id','name','email','city'],['1','Ada Whitfield','ada@example.com','Leeds'],['2','Bruno Sala','bruno@example.com','Milan'],['3','Chidi Okafor','NULL','Lagos'],['4','Dara Novak','dara@example.com','NULL'],['5','Emi Tanaka','emi@example.com','Osaka']]},
  {name:'products', rows:20, cols:[['id','integer','PK'],['name','text',''],['category','text',''],['price','numeric',''],['stock','integer',''],['active','boolean','']],
   preview:[['id','name','category','price'],['1','Aurora Wireless Earbuds','Electronics','59.00'],['2','Nimbus Headphones','Electronics','129.00'],['3','Linen Duvet Cover','Home','84.00'],['4','Cedar Desk Lamp','Home','42.50'],['5','Trail Runner Jacket','Outdoor','139.00']]},
  {name:'orders', rows:80, cols:[['id','integer','PK'],['customer_id','integer','FK'],['ordered_at','timestamp',''],['status','text','']],
   preview:[['id','customer_id','ordered_at','status'],['1001','1','2026-01-04 10:12','completed'],['1002','1','2026-01-19 16:40','completed'],['1003','2','2026-01-22 09:03','cancelled'],['1004','3','2026-02-02 14:20','completed'],['1005','1','2026-02-11 11:55','completed']]},
  {name:'order_items', rows:160, cols:[['id','integer','PK'],['order_id','integer','FK'],['product_id','integer','FK'],['quantity','integer',''],['unit_price','numeric','']],
   preview:[['id','order_id','product_id','quantity','unit_price'],['1','1001','1','2','59.00'],['2','1001','3','1','84.00'],['3','1002','5','1','139.00'],['4','1004','2','1','129.00'],['5','1005','1','3','59.00']]}
];

