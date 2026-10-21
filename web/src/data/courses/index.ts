import type { Course } from '../../types';
import { SQL_ANALYTICS_MODULES } from './sql-analytics';
import { JOINS_MODULES } from './joins';
import { SUBQUERIES_MODULES } from './subqueries';
import { FOUNDATIONS_MODULES } from './foundations';
import { PLANS_MODULES } from './plans';
import { SQLITE_ESSENTIALS_MODULES } from './sqlite-essentials';
import { SQLITE_IN_PRODUCTION_MODULES } from './sqlite-in-production';
import { POSTGRES_TO_SQLITE_MODULES } from './postgres-to-sqlite';

export {
  SQL_ANALYTICS_MODULES,
  JOINS_MODULES,
  SUBQUERIES_MODULES,
  FOUNDATIONS_MODULES,
  PLANS_MODULES,
  SQLITE_ESSENTIALS_MODULES,
  SQLITE_IN_PRODUCTION_MODULES,
  POSTGRES_TO_SQLITE_MODULES,
};

/* The first entry is the first course a new learner meets: SQLite runs in the
   browser with no server. Foundations is the first PostgreSQL course, and the
   rest follow from there. Array order is the signal, so keep it deliberate. */
export const COURSES: Course[] = [
  { id:'sqlite-essentials', title:'SQLite Essentials', domain:'SQLite', level:'Beginner \u2192 intermediate', minutes:99,
    source:'library', enrolled:false, built:true, dialect:'sqlite', modules:SQLITE_ESSENTIALS_MODULES,
    blurb:'The engine on its own terms: a database that is a file, storage classes and type affinity, the rowid, dates as ISO-8601 text, LIKE and GLOB, reading EXPLAIN QUERY PLAN, and an honest account of what SQLite cannot do.' },
  { id:'foundations', title:'SQL Foundations', domain:'Commerce & retail', level:'Beginner', minutes:100,
    source:'library', enrolled:false, built:true, dialect:'postgresql', modules:FOUNDATIONS_MODULES,
    blurb:'The clauses underneath every query: projection and DISTINCT, WHERE and boolean precedence, ORDER BY with LIMIT and OFFSET, NULL, IN/BETWEEN/LIKE, CASE, and aggregation with HAVING.' },
  { id:'sql-analytics', title:'SQL Analytics in the Real World', domain:'Commerce & retail', level:'Intermediate', minutes:120,
    source:'library', enrolled:false, built:true, dialect:'postgresql', modules:SQL_ANALYTICS_MODULES,
    blurb:'Go beyond basic aggregation: revenue reports, ranking, cohort retention and query reasoning on one realistic commerce dataset.' },
  { id:'joins', title:'Joins without guesswork', domain:'Commerce & retail', level:'Beginner → intermediate', minutes:75,
    source:'library', enrolled:false, built:true, dialect:'postgresql', modules:JOINS_MODULES,
    blurb:'Understand join behaviour through data: unmatched rows, duplicate keys, NULLs and the grain trap that inflates every total.' },
  { id:'plans', title:'PostgreSQL query plans', domain:'Query tuning', level:'Intermediate', minutes:90,
    source:'library', enrolled:false, built:true, dialect:'postgresql', dataset:'perf', modules:PLANS_MODULES,
    blurb:'Read EXPLAIN ANALYZE, understand index scans and selectivity, and reason about joins and row estimates.' },
  { id:'subqueries', title:'Subqueries and set operations', domain:'Commerce & retail', level:'Intermediate', minutes:70,
    source:'library', enrolled:false, built:true, dialect:'postgresql', modules:SUBQUERIES_MODULES,
    blurb:'Use one query as the input to another: scalar and list subqueries, correlated EXISTS, the NOT IN NULL trap, and set operations that compare whole result sets.' },
  { id:'sqlite-in-production', title:'SQLite in Production', domain:'SQLite', level:'Intermediate \u2192 advanced', minutes:100,
    source:'library', enrolled:false, built:true, dialect:'sqlite', modules:SQLITE_IN_PRODUCTION_MODULES,
    blurb:'The parts of SQLite that surprise application developers: one writer and WAL, the pragmas that are per-connection, STRICT tables, foreign keys that do not fire by default, JSON and generated columns, schema changes with user_version, and shipping the file.' },
  { id:'postgres-to-sqlite', title:'Postgres to SQLite', domain:'SQLite', level:'Intermediate \u2192 advanced', minutes:100,
    source:'library', enrolled:false, built:true, dialect:'sqlite', modules:POSTGRES_TO_SQLITE_MODULES,
    blurb:'A translation course: :: becomes CAST, DATE_TRUNC becomes strftime, DISTINCT ON becomes a window, INTERVAL becomes date modifiers, numeric becomes REAL, serial becomes the rowid — plus the short honest list of PostgreSQL constructs that have no SQLite port at all.' }
];
