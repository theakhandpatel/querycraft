package ai

import "fmt"

/* schemas.go owns every JSON Schema the gateway advertises to a provider.
 *
 * Each schema is a hint, never the gate: validateArtifact, validateCourseChapter
 * and validateCourseOutline decide, so a provider that ignores its schema is
 * still refused or accepted by the same rules. The schemas sit in one file so a
 * rule stated in a prompt and a rule advertised in a schema are read together.
 *
 * course_agent.go advertises courseOutlineSchema, courseChapterSchema and
 * finishCourseSchema; pipeline.go advertises challengeSchema; and
 * course_agent_tools.go advertises runSQLSchema for its read-only tool.
 */

// courseOutlineSchema is the JSON Schema of the outline call. It is a separate
// schema from courseChapterSchema because the outline is a different object:
// asking one question and validating a different one is how a repair round gets
// spent explaining a shape nobody asked for.
const courseOutlineSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "courseOutline",
  "type": "object",
  "required": ["title", "subtitle", "level", "objectives", "setup", "chapters"],
  "properties": {
    "title": {"type": "string", "minLength": 1},
    "subtitle": {"type": "string", "minLength": 1},
    "level": {"type": "string", "minLength": 1},
    "objectives": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}},
    "setup": {
      "type": "object",
      "required": ["dataset", "schema_version", "seed_version", "statements"],
      "properties": {
        "dataset": {"type": "string", "minLength": 1},
        "schema_version": {"type": "integer"},
        "seed_version": {"type": "integer"},
        "statements": {"type": "array", "items": {"type": "string", "minLength": 1}}
      }
    },
    "chapters": {
      "type": "array", "minItems": 1, "maxItems": 8,
      "items": {
        "type": "object",
        "required": ["id", "title", "summary", "objectives", "exercises"],
        "properties": {
          "id": {"type": "string", "minLength": 1},
          "n": {"type": "integer"},
          "title": {"type": "string", "minLength": 1},
          "summary": {"type": "string", "minLength": 1},
          "kind": {"type": "string", "minLength": 1},
          "minutes": {"type": "integer"},
          "objectives": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}},
          "exercises": {"type": "integer"}
        }
      }
    }
  }
}`

// courseChapterSchema is the JSON Schema of one chapter. It is the arguments
// schema of the save_chapter function the course agent advertises. One source,
// because a rule stated one place and applied another is a rule that will
// differ.
//
// Like challengeSchema it is a hint rather than the gate; validateCourseChapter
// decides. The bounds are part of the hint: a schema that cannot describe a
// one-line section removes the repair round trip that would otherwise be spent
// explaining why it is not teaching. The three bounds below are rendered from
// the same constants the gate reads, so the hint and the gate cannot drift.
const courseChapterSchemaFormat = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "courseChapter",
  "type": "object",
  "required": ["id", "title", "summary", "objectives", "sections", "exercises"],
  "properties": {
    "id": {"type": "string", "minLength": 1},
    "n": {"type": "integer"},
    "title": {"type": "string", "minLength": 1},
    "summary": {"type": "string", "minLength": 1},
    "kind": {"type": "string", "minLength": 1},
    "minutes": {"type": "integer"},
    "objectives": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}},
    "sections": {
      "type": "array", "minItems": 1, "maxItems": %d,
      "items": {
        "type": "object",
        "required": ["title", "body"],
        "properties": {
          "title": {"type": "string", "minLength": 1},
          "body": {"type": "string", "minLength": %d}
        }
      }
    },
    "exercises": {
      "type": "array", "minItems": 1, "maxItems": %d,
      "items": {
        "type": "object",
        "required": ["id", "title", "prompt", "difficulty", "objectives", "hints", "reference_solution", "cross_check_solution", "result_contract"],
        "properties": {
          "id": {"type": "string", "minLength": 1},
          "title": {"type": "string", "minLength": 1},
          "prompt": {"type": "string", "minLength": 1, "description": "The learner-facing task. It must name every result column the reference returns, using the same names: the grader compares column names exactly."},
          "difficulty": {"enum": ["beginner", "intermediate", "advanced"]},
          "type": {"enum": ["complete", "write", "modify", "predict", "fix", "combine", "solve"]},
          "objectives": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}},
          "hints": {"type": "array", "maxItems": 3, "items": {"type": "string", "minLength": 1}},
          "reference_solution": {"type": "string", "minLength": 1},
          "cross_check_solution": {"type": "string", "minLength": 1},
          "result_contract": {
            "type": "object",
            "required": ["columns", "column_order", "row_order", "duplicates", "numeric_tolerance"],
            "properties": {
              "columns": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}},
              "column_order": {"enum": ["exact", "ignored"]},
              "row_order": {"enum": ["exact", "ignored"]},
              "duplicates": {"enum": ["preserved", "ignored"]},
              "numeric_tolerance": {"type": "number"}
            }
          }
        }
      }
    }
  }
}`

var courseChapterSchema = fmt.Sprintf(courseChapterSchemaFormat, maxCourseSections, minSectionChars, maxCourseExercises)

// finishCourseSchema is the arguments schema of finish_course. It carries
// nothing the gateway needs, which is the point: closing a course must not be a
// second place the course is described, because two descriptions drift.
const finishCourseSchema = `{
  "type": "object",
  "properties": {
    "summary": {"type": "string", "description": "One sentence on what the course now teaches."}
  }
}`

// challengeSchema is the JSON Schema the pipeline advertises in
// ProviderRequest.Schema. It is a subset of contracts/ai.schema.json: the
// language-neutral contract is the source of truth, and this string keeps the
// request self-describing without reading a file at runtime. A provider that
// ignores it is still validated by validateArtifact, so the schema is a hint,
// never the gate.
//
// The bounds are part of the hint. Without minLength a model may satisfy the
// schema with `"statements": [""]`, and validateArtifact refuses the bundle only
// later, after the repair budget has been spent re-asking the same question. A
// schema that cannot describe an empty statement removes that round trip, and a
// provider that enforces json_schema removes the possibility.
//
// `setup.statements` carries minLength on its items but no minItems: an empty
// list is the correct answer for a challenge over the sealed dataset (see
// SetupPlan). Demanding one entry would only push a model into inventing a
// statement the environment already provides: a `CREATE TABLE customers`
// collides with the loaded dataset, and a `DROP` is refused.
const challengeSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "challengeArtifact",
  "type": "object",
  "required": ["title", "prompt", "difficulty", "objectives", "hints", "setup", "reference_solution", "cross_check_solution", "result_contract"],
  "properties": {
    "title": {"type": "string", "minLength": 1},
    "prompt": {"type": "string", "minLength": 1},
    "difficulty": {"enum": ["beginner", "intermediate", "advanced"]},
    "objectives": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}},
    "hints": {"type": "array", "items": {"type": "string", "minLength": 1}},
    "setup": {
      "type": "object",
      "required": ["dataset", "schema_version", "seed_version", "statements"],
      "properties": {
        "dataset": {"type": "string", "minLength": 1},
        "schema_version": {"type": "integer"},
        "seed_version": {"type": "integer"},
        "statements": {"type": "array", "items": {"type": "string", "minLength": 1}}
      }
    },
    "reference_solution": {"type": "string", "minLength": 1},
    "cross_check_solution": {"type": "string", "minLength": 1},
    "result_contract": {
      "type": "object",
      "required": ["columns", "column_order", "row_order", "duplicates", "numeric_tolerance"],
      "properties": {
        "columns": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}},
        "column_order": {"enum": ["exact", "ignored"]},
        "row_order": {"enum": ["exact", "ignored"]},
        "duplicates": {"enum": ["preserved", "ignored"]},
        "numeric_tolerance": {"type": "number"}
      }
    }
  }
}`

// runSQLSchema is the arguments schema of the run_sql tool, the one tool the
// course agent advertises that is not a chapter or the close. It takes one
// string and nothing else; the tool decides what a query may read by running it
// through the engine with a small result cap.
const runSQLSchema = `{"type":"object","properties":{"sql":{"type":"string","description":"The SQL to run"}},"required":["sql"]}`
