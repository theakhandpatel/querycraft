package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/sky-valley/pi/agent"
	"github.com/sky-valley/pi/ai"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// FakeProvider is the deterministic provider the app ships as its built-in
// provider. It needs no key and no network, so the pipeline, the evaluator and
// the exit gate run out of the box. It is not a mock of a model: its output is
// real SQL against the commerce dataset (backend/internal/dataset/schema.sql),
// so every stage that runs its SQL runs the same statements a learner would.
// Every reply is a pure function of the request, which is what makes the
// pipeline's determinism testable.
//
// It imports no network package, on purpose: a deterministic provider that
// could dial out would turn "no key, no network" into a claim rather than a
// fact.
type FakeProvider struct{}

// NewFakeProvider returns the built-in provider.
func NewFakeProvider() *FakeProvider { return &FakeProvider{} }

// Descriptor implements Provider.
func (f *FakeProvider) Descriptor() provider.ProviderDescriptor {
	return provider.ProviderDescriptor{
		Name:          "fake",
		Model:         "fake-v1",
		Version:       "1",
		Deterministic: true,
		Configured:    true,
	}
}

// Complete implements Provider. The reply depends only on the request, so the
// same request always yields the same bundle.
func (f *FakeProvider) Complete(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	if err := ctx.Err(); err != nil {
		return provider.ProviderResponse{}, err
	}
	start := time.Now()

	var (
		text string
		err  error
	)
	switch req.Kind {
	case "challenge", "course", "repair":
		// A repair is the same generator: the request carries the original
		// topics and the failure, so re-generating from the prompt produces a
		// corrected bundle for the topic the pipeline asked for.
		//
		// A repair of a course is a course: the prompt is what decides which
		// generator runs, so the same rule that produced the plan is the rule
		// that re-produces it.
		//
		// The chapter conversation does not come through here: it speaks
		// pi's loop through CourseStreamFn.
		if req.Kind == "course" || strings.Contains(req.Prompt, coursePromptMarker) {
			text, err = f.courseOutline(req.Prompt)
		} else {
			text, err = f.bundle(req.Prompt)
		}
	case "tutor":
		text = tutorText(req)
	case "critique":
		text = critiqueText(req)
	default:
		return provider.ProviderResponse{}, fmt.Errorf("fake provider: unsupported kind %q", req.Kind)
	}
	if err != nil {
		return provider.ProviderResponse{}, err
	}
	if req.MaxOutputBytes > 0 && len(text) > req.MaxOutputBytes {
		return provider.ProviderResponse{}, fmt.Errorf("fake provider: reply of %d bytes exceeds the %d-byte budget", len(text), req.MaxOutputBytes)
	}
	return provider.ProviderResponse{
		Text:      text,
		LatencyMS: float64(time.Since(start).Microseconds()) / 1000,
	}, nil
}

// bundle renders one challenge artifact as JSON. Anything the pipeline stamps
// (provenance, final version) is left at its zero value here and overwritten
// later; what the provider supplies is the content.
func (f *FakeProvider) bundle(prompt string) (string, error) {
	seed := hashPrompt(prompt)
	topic := pickTopic(prompt, seed)
	difficulty := difficultyFromText(prompt)

	artifact := schema.Artifact{
		ID:                 fmt.Sprintf("fake-%s-%04x", topic.name, seed&0xffff),
		Version:            1,
		Dialect:            "postgresql",
		Type:               "write",
		Title:              topic.title,
		Prompt:             topic.prompt + scenarioSuffix(seed),
		Difficulty:         difficulty,
		Objectives:         topic.objectives,
		Hints:              topic.hints,
		Setup:              schema.SetupPlan{Dataset: "commerce", SchemaVersion: 1, SeedVersion: 1, Statements: []string{}},
		ReferenceSolution:  topic.reference,
		CrossCheckSolution: topic.crossCheck,
		ResultContract: schema.ResultContract{
			Columns:          topic.columns,
			ColumnOrder:      "exact",
			RowOrder:         "exact",
			Duplicates:       "preserved",
			NumericTolerance: 0,
		},
		Checks: []schema.Check{},
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		return "", fmt.Errorf("fake provider: encode bundle: %w", err)
	}
	return string(raw), nil
}

// hashPrompt seeds every choice. FNV is enough: this is a determinism device,
// not a security boundary.
func hashPrompt(prompt string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(prompt))))
	return h.Sum64()
}

// scenarioSuffix varies the wording with the seed so two different requests do
// not produce byte-identical prompts, while one request always does.
func scenarioSuffix(seed uint64) string {
	scenarios := []string{
		" The store owner wants the answer as a table they can read top to bottom.",
		" A colleague will paste your query into a report, so the columns matter.",
		" Keep the result stable: the same query must return the same rows in the same order.",
		" Think about which table holds the fact you need before you write the SELECT.",
	}
	return "\n\n" + scenarios[seed%uint64(len(scenarios))]
}

func difficultyFromText(prompt string) schema.Difficulty {
	lower := strings.ToLower(prompt)
	switch {
	case strings.Contains(lower, "advanced"):
		return schema.Advanced
	case strings.Contains(lower, "beginner"):
		return schema.Beginner
	default:
		return schema.Intermediate
	}
}

// fakeTopic is one authored exercise the generator can emit. The reference and
// the cross-check answer the same question in different shapes, which is what
// the cross-check stage exists to test.
type fakeTopic struct {
	name       string
	keywords   []string
	title      string
	prompt     string
	reference  string
	crossCheck string
	columns    []string
	objectives []string
	hints      []string
}

// fakeTopics is the generator's whole content. The SQL is real: the tables and
// columns are the commerce dataset's (backend/internal/dataset/schema.sql), so a
// stage that runs these statements is running the same SQL a learner would.
var fakeTopics = []fakeTopic{
	{
		name:     "joins",
		keywords: []string{"join", "relationship", "foreign key"},
		title:    "Customers and how many orders they placed",
		prompt:   "List each customer who has placed at least one order, with their name and their number of orders, most orders first. Break ties by customer name. Name the columns customer_name and order_count.",
		reference: `SELECT c.name AS customer_name, count(o.id) AS order_count
FROM "dataset"."customers" c
JOIN "dataset"."orders" o ON o.customer_id = c.id
GROUP BY c.name
ORDER BY order_count DESC, customer_name ASC`,
		crossCheck: `SELECT c.name AS customer_name,
       (SELECT count(*) FROM "dataset"."orders" o WHERE o.customer_id = c.id) AS order_count
FROM "dataset"."customers" c
WHERE EXISTS (SELECT 1 FROM "dataset"."orders" o WHERE o.customer_id = c.id)
ORDER BY order_count DESC, customer_name ASC`,
		columns:    []string{"customer_name", "order_count"},
		objectives: []string{"Join two tables on a foreign key.", "Aggregate per group with GROUP BY.", "Order by an aggregate and break ties deterministically."},
		hints: []string{
			"Start from the customers table and think about which column links it to orders.",
			"count(o.id) counts matched orders; count(*) would also count rows with no orders if you used a LEFT JOIN.",
			"Use GROUP BY c.name and then ORDER BY the aggregate with a name tie-break.",
		},
	},
	{
		name:     "aggregation",
		keywords: []string{"aggregat", "group by", "group", "count", "average", "avg", "sum"},
		title:    "Products per category and their average price",
		prompt:   "For each product category, return the category, how many products it contains, and the average price rounded to two decimals, highest average price first. Break ties by category name. Name the columns category, product_count and avg_price.",
		reference: `SELECT p.category AS category, count(*) AS product_count, round(avg(p.price), 2) AS avg_price
FROM "dataset"."products" p
GROUP BY p.category
ORDER BY avg_price DESC, category ASC`,
		crossCheck: `SELECT DISTINCT p.category AS category,
       count(*) OVER (PARTITION BY p.category) AS product_count,
       round(avg(p.price) OVER (PARTITION BY p.category), 2) AS avg_price
FROM "dataset"."products" p
ORDER BY avg_price DESC, category ASC`,
		columns:    []string{"category", "product_count", "avg_price"},
		objectives: []string{"Aggregate a table per group.", "Round a numeric average.", "Order groups by an aggregate."},
		hints: []string{
			"GROUP BY categories the rows; every column you SELECT must be grouped or aggregated.",
			"count(*) counts rows in the group; avg() ignores NULLs, and price is never NULL here.",
			"round(avg(price), 2) keeps the answer readable and comparable.",
		},
	},
	{
		name:     "windows",
		keywords: []string{"window", "rank", "partition by", "dense_rank", "row_number"},
		title:    "Rank customers by total order value",
		prompt:   "Rank each customer who has ordered by the total value of their orders (quantity times unit price), highest first. Show the customer name, the total, and the rank, breaking ties by name. Name the columns customer_name, total_value and value_rank.",
		reference: `SELECT t.customer_name,
       t.total_value,
       rank() OVER (ORDER BY t.total_value DESC) AS value_rank
FROM (
    SELECT c.name AS customer_name,
           sum(oi.quantity * oi.unit_price) AS total_value
    FROM "dataset"."customers" c
    JOIN "dataset"."orders" o ON o.customer_id = c.id
    JOIN "dataset"."order_items" oi ON oi.order_id = o.id
    GROUP BY c.name
) t
ORDER BY t.total_value DESC, t.customer_name ASC`,
		crossCheck: `SELECT c.name AS customer_name,
       (SELECT sum(oi.quantity * oi.unit_price)
          FROM "dataset"."orders" o
          JOIN "dataset"."order_items" oi ON oi.order_id = o.id
         WHERE o.customer_id = c.id) AS total_value,
       rank() OVER (ORDER BY (SELECT sum(oi.quantity * oi.unit_price)
          FROM "dataset"."orders" o
          JOIN "dataset"."order_items" oi ON oi.order_id = o.id
         WHERE o.customer_id = c.id) DESC) AS value_rank
FROM "dataset"."customers" c
WHERE EXISTS (SELECT 1 FROM "dataset"."orders" o2 WHERE o2.customer_id = c.id)
ORDER BY total_value DESC, customer_name ASC`,
		columns:    []string{"customer_name", "total_value", "value_rank"},
		objectives: []string{"Sum a computed value across a join.", "Use a window function to rank rows.", "Keep the window source deterministic."},
		hints: []string{
			"First build the per-customer total, then rank that result; a window function cannot see an aggregate in the same SELECT in every engine.",
			"rank() OVER (ORDER BY total DESC) leaves gaps after ties; dense_rank() does not.",
			"The inner query groups by c.name, so the outer SELECT sees one row per customer.",
		},
	},
	{
		name:     "ctes",
		keywords: []string{"cte", "common table", "with clause", "with statement"},
		title:    "The three highest-value orders",
		prompt:   "Using a common table expression, return the three orders with the largest total value (quantity times unit price), with the order id, the customer name and the order total. Break ties by order id. Name the columns order_id, customer_name and order_total.",
		reference: `WITH order_totals AS (
    SELECT o.id AS order_id, c.name AS customer_name,
           sum(oi.quantity * oi.unit_price) AS order_total
    FROM "dataset"."orders" o
    JOIN "dataset"."customers" c ON c.id = o.customer_id
    JOIN "dataset"."order_items" oi ON oi.order_id = o.id
    GROUP BY o.id, c.name
)
SELECT order_id, customer_name, order_total
FROM order_totals
ORDER BY order_total DESC, order_id ASC
LIMIT 3`,
		crossCheck: `SELECT o.id AS order_id, c.name AS customer_name,
       (SELECT sum(oi.quantity * oi.unit_price)
          FROM "dataset"."order_items" oi
         WHERE oi.order_id = o.id) AS order_total
FROM "dataset"."orders" o
JOIN "dataset"."customers" c ON c.id = o.customer_id
WHERE EXISTS (SELECT 1 FROM "dataset"."order_items" oi2 WHERE oi2.order_id = o.id)
ORDER BY order_total DESC, order_id ASC
LIMIT 3`,
		columns:    []string{"order_id", "customer_name", "order_total"},
		objectives: []string{"Write a CTE with WITH.", "Aggregate a joined line-item table.", "Apply LIMIT after ordering."},
		hints: []string{
			"A CTE names a query you can SELECT from as if it were a table.",
			"The total for an order is sum(quantity * unit_price) over its order_items.",
			"Order before you limit, or LIMIT will keep arbitrary rows.",
		},
	},
}

// pickTopic chooses the topic whose keyword appears earliest in the prompt, so
// the request's own topics drive the outcome, and falls back to the seed. Fixed
// iteration order keeps the tie-break deterministic.
func pickTopic(prompt string, seed uint64) fakeTopic {
	lower := strings.ToLower(prompt)
	best, bestAt := -1, -1
	for i, t := range fakeTopics {
		for _, kw := range t.keywords {
			at := strings.Index(lower, kw)
			if at < 0 {
				continue
			}
			if bestAt < 0 || at < bestAt {
				best, bestAt = i, at
			}
		}
	}
	if best >= 0 {
		return fakeTopics[best]
	}
	return fakeTopics[seed%uint64(len(fakeTopics))]
}

// tutorText is a plain-text hint. It never contains SQL from the request other
// than the learner's own, and it has no access to a reference solution.
func tutorText(req provider.ProviderRequest) string {
	switch parseHintLevel(req.Prompt) {
	case 0:
		return "Read the prompt again and name, in words, the single table that holds the fact you need. No SQL yet."
	case 1:
		return "Look at the columns in the tables you chose. Which column links the two tables you are reading from?"
	case 2:
		return "The shape is close. Check what your GROUP BY produces: is one row per group, and is every other SELECT column aggregated?"
	default:
		return "Reread the ORDER BY. If two rows tie, the database may pick either order; add a second, unique key to the sort."
	}
}

// critiqueText reviews the learner's query without seeing a reference solution.
func critiqueText(req provider.ProviderRequest) string {
	return "The query runs against the commerce dataset. Check the join keys and the grouping first: those are where most wrong answers start. Then confirm the ORDER BY leaves no ties."
}

/* ---------- schema advertised to a provider ---------- */

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

/* ---------- the deterministic course ---------- */

// coursePlanShape is the authored course the built-in provider serves, as a
// plan. Both the outline call and every chapter call are derived from it, so
// the outline the learner sees and the chapters that are written cannot
// describe different courses. Everything here is a pure function of the prompt,
// which is what makes the pipeline's determinism testable.
func (f *FakeProvider) coursePlanShape(prompt string) schema.CoursePlan {
	seed := hashPrompt(prompt)
	difficulty := difficultyFromText(prompt)
	// Three chapters of one exercise each: enough to be a course, small enough
	// that a run stays fast.
	topics := []fakeTopic{fakeTopics[seed%uint64(len(fakeTopics))], fakeTopics[(seed+1)%uint64(len(fakeTopics))], fakeTopics[(seed+2)%uint64(len(fakeTopics))]}
	seen := map[string]bool{}
	unique := make([]fakeTopic, 0, 3)
	for _, t := range topics {
		if seen[t.name] {
			continue
		}
		seen[t.name] = true
		unique = append(unique, t)
	}
	// A seed whose three picks collide leaves one or two chapters; the plan
	// stays valid, and a shorter course is a real outcome rather than a bug.
	plan := schema.CoursePlan{
		Title:      courseTitle(prompt, seed),
		Subtitle:   "Chapter by chapter, from the shape of the data to a question you could be asked in an interview.",
		Level:      string(difficulty),
		Dialect:    "postgresql",
		Objectives: []string{"Read the commerce schema and know which table holds which fact.", "Answer a question with one statement that runs, returns the right columns, and can be explained."},
		Setup:      schema.SetupPlan{Dataset: "commerce", SchemaVersion: 1, SeedVersion: 1, Statements: []string{}},
	}
	for i, t := range unique {
		plan.Chapters = append(plan.Chapters, fakeChapter(t, i+1, string(difficulty), seed))
	}
	return plan
}

// fakeChapter fabricates the chapter the built-in provider writes for one
// topic: its sections, and one exercise with a reference solution, an
// independent cross-check, and a result contract naming the exact columns.
func fakeChapter(t fakeTopic, n int, difficulty string, seed uint64) schema.CourseChapter {
	return schema.CourseChapter{
		ID:         fmt.Sprintf("gen.%s", t.name),
		N:          n,
		Title:      t.title,
		Summary:    t.prompt,
		Kind:       "Practice",
		Minutes:    15,
		Objectives: t.objectives,
		Sections: []schema.CourseSection{
			{Title: "The shape of the question", Body: courseSectionIntro(t)},
			{Title: "How the pieces fit", Body: courseSectionDiagram(t)},
		},
		Exercises: []schema.CourseExercise{{
			ID:                 fmt.Sprintf("gen.%s.1", t.name),
			Title:              t.title,
			Prompt:             t.prompt + scenarioSuffix(seed),
			Difficulty:         schema.Difficulty(difficulty),
			Type:               "write",
			Objectives:         t.objectives,
			Hints:              t.hints,
			ReferenceSolution:  t.reference,
			CrossCheckSolution: t.crossCheck,
			ResultContract: schema.ResultContract{
				Columns:          t.columns,
				ColumnOrder:      "exact",
				RowOrder:         "exact",
				Duplicates:       "preserved",
				NumericTolerance: 0,
			},
			Checks: []schema.Check{},
		}},
	}
}

// courseOutline renders the outline the agent's first call asks for, derived
// from the same plan the chapters come from.
func (f *FakeProvider) courseOutline(prompt string) (string, error) {
	plan := f.coursePlanShape(prompt)
	outline := CourseOutline{
		Title:      plan.Title,
		Subtitle:   plan.Subtitle,
		Level:      plan.Level,
		Objectives: plan.Objectives,
		Setup:      plan.Setup,
	}
	for i, ch := range plan.Chapters {
		outline.Chapters = append(outline.Chapters, OutlineChapter{
			ID:         ch.ID,
			N:          i + 1,
			Title:      ch.Title,
			Summary:    ch.Summary,
			Kind:       ch.Kind,
			Minutes:    ch.Minutes,
			Objectives: ch.Objectives,
			Exercises:  len(ch.Exercises),
		})
	}
	raw, err := json.Marshal(outline)
	if err != nil {
		return "", fmt.Errorf("fake provider: encode outline: %w", err)
	}
	return string(raw), nil
}

// Course is the built-in provider's answer to the course-agent capability:
// no key, a self-describing model record, and the chapter conversation served
// by its own logic rather than by a network call.
func (f *FakeProvider) Course() provider.CourseCapability {
	return provider.CourseCapability{
		Model:    &ai.Model{ID: "built-in", Name: "built-in", Provider: "built-in"},
		Key:      func(string) string { return "" },
		StreamFn: f.CourseStreamFn(),
	}
}

// CourseStreamFn serves the chapter conversation. Its policy is fixed and
// deterministic: it reads the resolved chapters out of the tool results the
// gateway already sent, then answers with the first outline chapter that is not
// resolved, or with finish_course once every chapter in its own plan is
// resolved. It is a pure function of the messages, exactly like every other
// reply this provider makes, so a run is reproducible from its request alone
// and needs no key.
func (f *FakeProvider) CourseStreamFn() agent.StreamFn {
	return func(ctx context.Context, model *ai.Model, req ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		if err := ctx.Err(); err != nil {
			// The run budget ended: abort the way a real provider does.
			return ai.ErrorStream(model, err)
		}
		resolved := map[string]bool{}
		for _, m := range req.Messages {
			if tr, ok := m.(ai.ToolResultMessage); ok {
				var result struct {
					Chapter string `json:"chapter"`
					Status  string `json:"status"`
				}
				if json.Unmarshal([]byte(textFromContent(tr.Content)), &result) == nil {
					if result.Chapter != "" && (result.Status == ChapterVerified || result.Status == ChapterDropped) {
						resolved[result.Chapter] = true
					}
				}
			}
		}
		outline, ok := outlineFromGoal(req.Messages)
		msg := &ai.AssistantMessage{Model: "built-in"}
		if !ok {
			// No outline in the goal prompt: a conversation this provider does
			// not know how to continue. A plain closing turn ends the run; the
			// gateway reports the chapters it never received.
			msg.Content = ai.ContentList{ai.TextContent{Text: "I cannot find the agreed outline in the goal prompt."}}
			msg.StopReason = ai.StopStop
		} else {
			next := -1
			for i, och := range outline.Chapters {
				if !resolved[och.ID] {
					next = i
					break
				}
			}
			if next < 0 {
				msg.Content = ai.ContentList{ai.ToolCall{
					ID:   "call_finish_course",
					Name: toolFinishCourse,
					Arguments: map[string]any{
						"summary": "Every chapter that was written verified against the dataset.",
					},
				}}
				msg.StopReason = ai.StopToolUse
			} else {
				och := outline.Chapters[next]
				msg.Content = ai.ContentList{ai.ToolCall{
					ID:        "call_" + och.ID,
					Name:      toolSaveChapter,
					Arguments: fakeChapterArgs(och, next+1, string(difficultyFromText(firstUserText(req.Messages)))),
				}}
				msg.StopReason = ai.StopToolUse
			}
		}
		s := ai.NewAssistantMessageEventStream()
		go func() {
			s.Push(ai.AssistantMessageEvent{Type: ai.EventStart, Partial: &ai.AssistantMessage{}})
			s.Push(ai.AssistantMessageEvent{Type: ai.EventDone, Reason: msg.StopReason, Message: msg})
			s.End()
		}()
		return s
	}
}

// outlineFromGoal extracts the outline JSON the goal prompt embeds.
func outlineFromGoal(messages []ai.Message) (CourseOutline, bool) {
	for _, m := range messages {
		if um, ok := m.(ai.UserMessage); ok {
			if outline, ok := outlineJSONFromGoal(textFromContent(um.Content)); ok {
				return outline, true
			}
		}
	}
	return CourseOutline{}, false
}

// firstUserText returns the text of the conversation's first user message.
func firstUserText(messages []ai.Message) string {
	for _, m := range messages {
		if um, ok := m.(ai.UserMessage); ok {
			return textFromContent(um.Content)
		}
	}
	return ""
}

// outlineJSONFromGoal extracts the outline JSON body and decodes it.
func outlineJSONFromGoal(text string) (CourseOutline, bool) {
	i := strings.Index(text, goalOutlineMarker)
	if i < 0 {
		return CourseOutline{}, false
	}
	body := text[i+len(goalOutlineMarker):]
	j := strings.Index(body, goalOutlineEnd)
	if j < 0 {
		return CourseOutline{}, false
	}
	var outline CourseOutline
	if json.Unmarshal([]byte(body[:j]), &outline) != nil {
		return CourseOutline{}, false
	}
	return outline, true
}

// fakeChapterArgs fabricates the save_chapter arguments for one outline
// chapter. The topic table is keyed by the chapter id's topic name, the same
// convention the outline generator used, so the chapter content matches what
// the outline call derived; an unknown topic still produces a chapter that
// carries the outline's own fields.
func fakeChapterArgs(och OutlineChapter, n int, difficulty string) map[string]any {
	id := och.ID
	topic, _ := strings.CutPrefix(id, "gen.")
	var ch schema.CourseChapter
	if t, ok := topicByName(topic); ok {
		ch = fakeChapter(t, n, difficulty, hashPrompt(id))
	} else {
		ch = fakeChapter(fakeTopic{name: topic, title: och.Title, prompt: och.Summary, objectives: och.Objectives}, n, difficulty, hashPrompt(id))
	}
	ch.ID, ch.N, ch.Title, ch.Summary, ch.Objectives = id, n, och.Title, och.Summary, och.Objectives
	raw, err := json.Marshal(ch)
	if err != nil {
		return map[string]any{"id": id}
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return map[string]any{"id": id}
	}
	return out
}

// topicByName finds the topic table entry a chapter id names.
func topicByName(name string) (fakeTopic, bool) {
	for _, t := range fakeTopics {
		if t.name == name {
			return t, true
		}
	}
	return fakeTopic{}, false
}

// courseTitle names the course from the learner's own goal, so two different
// requests do not read as the same course.
func courseTitle(prompt string, seed uint64) string {
	goal := goalFromPrompt(prompt)
	if goal == "" {
		return "A course in analytical SQL"
	}
	if len(goal) > 70 {
		goal = strings.TrimSpace(goal[:67]) + "\u2026"
	}
	return "SQL for " + goal
}

// goalFromPrompt pulls the learner's own words back out of the rendered request.
// The built-in provider reads the same prompt a model would; it is not handed
// the request through a side channel.
func goalFromPrompt(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Goal: "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func courseSectionIntro(t fakeTopic) string {
	return fmt.Sprintf(`Every question in this chapter is answered by one statement, and the statement always has the same three parts: a row source, a grouping, and an order.

- **Row source.** Start from the table that owns the fact you are reporting on — orders for order counts, products for prices. Reading from the wrong table is the most common way a correct-looking query gives a wrong number.
- **Grouping.** Decide the grain first: one row per customer, per category, per month. GROUP BY is the grain written down.
- **Order.** A result set has no order until you ask for one. When you sort by a number, break ties by a name so the top of the list does not move between runs.

%s`, t.prompt)
}

func courseSectionDiagram(t fakeTopic) string {
	return fmt.Sprintf("Here is the shape of the work, from the question to the rows:\n\n```mermaid\nflowchart LR\n  Q[%s] --> S{Which table owns the fact?}\n  S -->|orders| O[Join orders to customers]\n  S -->|prices| P[Read products]\n  O --> G[GROUP BY the grain]\n  P --> G\n  G --> R[ORDER BY the measure, ties by name]\n  R --> A[Check the columns and their order]\n```\n\nThe last step is the one worth practising: the answer is graded on the columns you name, their order, and the rows you return — not on the text of your query. Two different queries that agree are both correct.", t.title)
}
