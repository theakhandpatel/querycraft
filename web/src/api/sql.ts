/* The one place SQL comments are stripped before a statement is examined.
   Both engines call it to tell a blank query from a real one, so a
   comment-only statement is rejected the same way everywhere. */

export function stripComments(sql: string): string {
  return sql
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/--[^\n]*/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}
