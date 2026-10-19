/* The course catalog, assembled from one module per course.

   Each course owns its chapters and exercises in ./courses/<id>.ts. The
   markdown reading content stays in ../content/<module>.md and is seeded into
   the local database from there. This file keeps the public catalog names that
   the rest of the app already imports. */

export { TABLES } from './tables';
export {
  COURSES,
  SQL_ANALYTICS_MODULES as MODULES,
  JOINS_MODULES,
  SUBQUERIES_MODULES,
  FOUNDATIONS_MODULES,
  PLANS_MODULES,
  SQLITE_ESSENTIALS_MODULES,
  SQLITE_IN_PRODUCTION_MODULES,
  POSTGRES_TO_SQLITE_MODULES,
} from './courses';
