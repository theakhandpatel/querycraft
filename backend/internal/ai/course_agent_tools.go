package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/querycraft/sqlquery/backend/internal/ai/transcript"
	"github.com/sky-valley/pi/agent"
	"github.com/sky-valley/pi/ai"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/*
 * The course agent, built on pi's agent.Agent.
 *
 * The tools are the pipeline's whole contract with the model. The pipeline
 * decides which AgentTool values the agent gets; the model calls them; pi runs
 * the loop; the Execute functions enforce grading policy. Provider calls, tool
 * call parsing, protocol nudges, retries and transcript recording all belong to
 * pi.
 *
 * The pipeline's role:
 *   1. open the scratch environment (a throwaway copy of the dataset),
 *   2. create the agent with the right tools,
 *   3. prompt it with the goal,
 *   4. wait for it to finish,
 *   5. check the build state and activate or refuse.
 *
 * Everything the model does in between goes through tool calls. The save_chapter
 * tool enforces the grading gate: nothing is stored unless it verified. The
 * run_sql tool lets the model test its own exercises before saving.
 */

// runCourseAgent runs the chapter conversation. The model and stream come from
// the provider's course capability, resolved once at the start of the run; one
// agent is built; pi's loop carries every turn after that. Tool call parsing,
// execution, retries and transcript folding belong to pi; the tools and the
// turn policy come from this package.
func (p *pipeline) runCourseAgent(ctx context.Context, build *courseBuild) *schema.Error {
	if p.svc.cfg.Depot == nil {
		return NewError(CodeUnavailable, schema.StageActivate,
			"The course could not be stored while it was being written.", "")
	}
	capability, ok := provider.CourseCapabilityOf(p.svc.cfg.Provider)
	if !ok {
		return NewError(CodeUnavailable, schema.StageRequestBundle,
			"The configured provider cannot run the course agent.", "")
	}

	ex, release, err := p.openScratch(ctx, &build.plan)
	if err != nil {
		return err
	}
	defer release()

	tools := p.courseAgentTools(build, ex)

	stream := capability.StreamFn
	if stream == nil {
		// No scripted stream: pi resolves the provider from the model record
		// and streams through its own adapters.
		stream = func(ctx context.Context, model *ai.Model, req ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			return ai.StreamSimple(ctx, model, ai.Context{Messages: req.Messages}, opts)
		}
	}
	stream = recoverToolCallsFromText(stream, courseToolNames(tools))

	// Turn policy: a turn that produced no tool calls is a protocol miss. Steer
	// one bounded correction and continue; the run budget caps the rest.
	nudges := 0
	// runStopped is the reason an assistant turn ended in an error or an
	// abort, when one did: the run died rather than finishing.
	runStopped := ""
	var ag *agent.Agent
	ag = agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentState{
			Model:        capability.Model,
			SystemPrompt: courseAgentSystemPrompt(),
			Tools:        tools,
		},
		StreamFn:   stream,
		GetApiKey:  capability.Key,
		MaxRetries: 2,
		TimeoutMs:  int(p.limits.Timeout.Milliseconds()),
		FinishTurn: func(ctx context.Context, turn agent.AgentTurnContext) agent.AgentTurnDecision {
			if len(turn.ToolResults) > 0 || nudges >= courseMaxNudges {
				return ""
			}
			nudges++
			ag.Steer(ai.UserMessage{Content: ai.ContentList{ai.TextContent{Text: courseNudgePrompt}}})
			return agent.TurnContinue
		},
	})

	ag.Subscribe(func(ctx context.Context, event agent.AgentEvent) error {
		rec := transcript.From(ctx)
		if event.Type != agent.EvMessageEnd {
			return nil
		}
		// Every message of the conversation passes through here exactly once:
		// the replayed turns a resumed build opens with, and each turn the loop
		// produces. Recording here keeps tool calls and their results paired,
		// so the transcript is a faithful read-back of the conversation.
		switch msg := event.Message.(type) {
		case *ai.AssistantMessage:
			rec.RecordResponse(assistantTurnResponse(*msg))
			// A turn that ended in an error or an abort is the run dying, not
			// the model finishing. The chapters that verified stay, as
			// `interrupted`, and the build route can resume them; activating a
			// silently smaller course would lose the rest for good.
			if msg.StopReason == ai.StopError || msg.StopReason == ai.StopAborted {
				runStopped = msg.ErrorMessage
				if runStopped == "" {
					runStopped = "the provider connection ended the run"
				}
			}
		case ai.ToolResultMessage:
			rec.RecordToolResult(msg.ToolCallID, textFromContent(msg.Content))
		case ai.UserMessage:
			rec.Message("user", textFromContent(msg.Content))
		case ai.SystemMessage:
			rec.Message("system", textFromContent(msg.Content))
		}
		return nil
	})

	// pi adds the system prompt to the transcript without emitting it as a
	// message event, so the recorder is told once, here.
	transcript.From(ctx).Message("system", courseAgentSystemPrompt())

	// The run budget is the conversation's deadline: pi owns the turn count,
	// the configuration owns the clock. A run that spends the budget ends
	// with whatever verified, and finalizeCourse refuses what did not.
	runCtx, cancel := context.WithTimeout(ctx, p.limits.Timeout)
	defer cancel()

	// The conversation opens with the goal, and a resumed build replays the
	// chapters it already has as the turns that put them there, so the model
	// sees what is done and is never asked to write it again.
	messages := []agent.AgentMessage{
		ai.UserMessage{Content: ai.ContentList{ai.TextContent{Text: courseAgentPrompt(build.request, build.outline)}}},
	}
	messages = append(messages, storedChapterTurns(build)...)
	promptErr := ag.PromptMessages(runCtx, messages)
	if promptErr != nil {
		return NewError(CodeUnavailable, schema.StageRequestBundle,
			"The course agent could not start.", engine.RedactSecrets(promptErr.Error()))
	}
	ag.WaitForIdle()

	if runStopped != "" {
		return NewError(CodeUnavailable, schema.StageRepair,
			"The run ended before the course was finished. The chapters it verified are kept; the course can be resumed.",
			engine.RedactSecrets(runStopped))
	}

	if !build.finished && !build.resolved() {
		p.mark(schema.StageRepair, schema.StateDone,
			fmt.Sprintf("the conversation ended with %d chapter(s) unwritten", len(build.missing())))
	}
	return nil
}

// courseMaxNudges caps the protocol-miss correction: a model that keeps
// answering with prose instead of tool calls ends the run rather than
// exhausting the budget one turn at a time.
const courseMaxNudges = 2

// courseNudgePrompt is the correction the agent is steered with after a turn
// that carried no tool call.
const courseNudgePrompt = "Your last reply was prose. Work only through tool calls: " +
	"run_sql to test a query against the dataset, save_chapter to store a finished chapter, " +
	"finish_course when every chapter in the outline is saved and accepted."

// courseToolNames collects the names the agent's tools are advertised under,
// the allowlist for the text-form tool call recovery.
func courseToolNames(tools []agent.AgentTool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		names[t.Name] = true
	}
	return names
}

// recoverToolCallsFromText wraps a StreamFn with a deterministic transport
// repair. Some OpenAI-compatible servers drop the tools field or answer with
// the tool call written as JSON in the message text. When a finished message
// carries no native tool calls, its text is scanned for one of the agent's tools
// written as JSON; a match becomes a native call, so the loop executes it. Text
// that does not parse as one of the agent's tools stays text.
func recoverToolCallsFromText(inner agent.StreamFn, names map[string]bool) agent.StreamFn {
	return func(ctx context.Context, model *ai.Model, req ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		src := inner(ctx, model, req, opts)
		out := ai.NewAssistantMessageEventStream()
		go func() {
			defer out.End()
			for ev, ok := src.Next(); ok; ev, ok = src.Next() {
				if ev.Type == ai.EventDone && ev.Message != nil && !messageHasToolCalls(ev.Message) {
					text := textFromContent(ev.Message.Content)
					if calls, rest := parseTextToolCalls(text, names); len(calls) > 0 {
						content := ai.ContentList{}
						if strings.TrimSpace(rest) != "" {
							content = append(content, ai.TextContent{Text: rest})
						}
						for _, c := range calls {
							content = append(content, c)
						}
						ev.Message.Content = content
						ev.Message.StopReason = ai.StopToolUse
					}
				}
				out.Push(ev)
			}
		}()
		return out
	}
}

func messageHasToolCalls(m *ai.AssistantMessage) bool {
	for _, c := range m.Content {
		if _, ok := c.(ai.ToolCall); ok {
			return true
		}
	}
	return false
}

// parseTextToolCalls scans the text for tool calls written as JSON objects
// with a name the agent advertises. Recognized shapes: a fenced code block,
// a bare object for the whole text, and an array of objects. It returns the
// recovered calls and the text left over after removing what was recovered.
func parseTextToolCalls(text string, names map[string]bool) ([]ai.ToolCall, string) {
	var calls []ai.ToolCall
	rest := text
	for _, cand := range jsonCandidates(text) {
		var obj map[string]any
		if json.Unmarshal([]byte(cand), &obj) != nil {
			continue
		}
		name, _ := obj["name"].(string)
		if !names[name] {
			continue
		}
		args := firstOf(obj, "arguments", "parameters", "input", "args")
		m, _ := args.(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		calls = append(calls, ai.ToolCall{
			ID:        fmt.Sprintf("recovered_%d", len(calls)+1),
			Name:      name,
			Arguments: m,
		})
		rest = strings.Replace(rest, cand, "", 1)
		if len(calls) >= 8 {
			break
		}
	}
	return calls, rest
}

// jsonCandidates lists the JSON substrings of the text worth trying, most
// likely first: fenced code blocks, then the trimmed whole text.
func jsonCandidates(text string) []string {
	var out []string
	for _, block := range fencedCodeBlocks(text) {
		out = append(out, block)
	}
	if trimmed := strings.TrimSpace(text); trimmed != "" && (trimmed[0] == '{' || trimmed[0] == '[') {
		out = append(out, trimmed)
	}
	return out
}

// fencedCodeBlocks extracts the contents of ```-fenced code blocks, in order.
func fencedCodeBlocks(text string) []string {
	var out []string
	for {
		start := strings.Index(text, "```")
		if start < 0 {
			return out
		}
		body := text[start+3:]
		body = strings.TrimPrefix(body, "json")
		body = strings.TrimPrefix(body, "JSON")
		end := strings.Index(body, "```")
		if end < 0 {
			return out
		}
		out = append(out, strings.TrimSpace(body[:end]))
		text = body[end+3:]
		if len(out) >= 8 {
			return out
		}
	}
}

// firstOf returns the first non-nil value under one of keys. The keyword list
// exists because servers label a tool call's arguments differently; whichever
// key is present is the one that carries them.
func firstOf(obj map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := obj[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// courseAgentTools returns the agent's tools. Each Execute function delegates
// to the pipeline's existing tool handlers, which enforce grading policy.
func (p *pipeline) courseAgentTools(build *courseBuild, ex engine.Executor) []agent.AgentTool {
	return []agent.AgentTool{
		{
			Name:        toolSaveChapter,
			Description: "Save one finished chapter of the course: its markdown sections and its exercises, using the id the outline gave it. The gateway runs every exercise against the real database and answers with what it measured, so a chapter that does not run comes back with the database's own error and must be sent again. Every exercise prompt must name each result column exactly as the reference solution returns it: the grader compares column names exactly, so a learner who writes a correct query with a different alias is refused.",
			Parameters:  jsonSchemaToPi(json.RawMessage(courseChapterSchema)),
			Execute: func(ctx context.Context, toolCallID string, params map[string]any, _ agent.ToolUpdateFunc) (agent.AgentToolResult, error) {
				// pi hands the tool its arguments already parsed. saveChapterTool
				// takes the raw JSON string, so marshalling here keeps the
				// argument decoding in one place for the live loop and for a test.
				args, err := json.Marshal(params)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				result := p.saveChapterTool(ctx, build, ex, string(args))
				return agent.AgentToolResult{
					Content: ai.ContentList{ai.TextContent{Text: result}},
				}, nil
			},
		},
		{
			Name:        toolFinishCourse,
			Description: "Close the course once every chapter in the outline has been saved and accepted.",
			Parameters:  jsonSchemaToPi(json.RawMessage(finishCourseSchema)),
			Execute: func(ctx context.Context, toolCallID string, params map[string]any, _ agent.ToolUpdateFunc) (agent.AgentToolResult, error) {
				// Closing the course ends the conversation: Terminate stops pi's
				// loop after this batch, so the run does not ask the model what
				// to do with a course that is already closed.
				result := p.finishCourseTool(build)
				return agent.AgentToolResult{
					Content:   ai.ContentList{ai.TextContent{Text: result}},
					Terminate: build.finished,
				}, nil
			},
		},
		{
			Name:        "run_sql",
			Description: "Run a read-only SQL query against the scratch environment (the commerce dataset). Use this to test your exercises before saving the chapter: run the reference solution, check the columns and rows are what the prompt asks for, then save.",
			Parameters:  jsonSchemaToPi(json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string","description":"The SQL to run"}},"required":["sql"]}`)),
			Execute: func(ctx context.Context, toolCallID string, params map[string]any, _ agent.ToolUpdateFunc) (agent.AgentToolResult, error) {
				sql, _ := params["sql"].(string)
				if strings.TrimSpace(sql) == "" {
					return agent.AgentToolResult{
						Content: ai.ContentList{ai.TextContent{Text: "sql is required."}},
					}, nil
				}
				// The tool is for checking a query, not for reading the dataset,
				// so the engine caps the result at 50 rows, 51200 bytes and 5 s.
				res := engine.Execute(ctx, ex, sql, engine.Limits{MaxRows: 50, MaxBytes: 51200, TimeoutMS: 5000})
				if res.Error.Code != "" {
					return agent.AgentToolResult{
						Content: ai.ContentList{ai.TextContent{Text: fmt.Sprintf("Error %s: %s", res.Error.Code, res.Error.Message)}},
					}, nil
				}
				// Each row becomes an object keyed by the result's own column
				// names, which is what the model compares its contract against.
				rows := make([]map[string]any, 0, len(res.Rows))
				for _, row := range res.Rows {
					r := map[string]any{}
					for i, col := range res.Columns {
						if i < len(row) {
							r[col.Name] = row[i]
						}
					}
					rows = append(rows, r)
				}
				out, _ := json.Marshal(rows)
				return agent.AgentToolResult{
					Content: ai.ContentList{ai.TextContent{Text: fmt.Sprintf("%d rows:\n%s", res.RowCount, string(out))}},
				}, nil
			},
		},
	}
}

// jsonSchemaToPi converts a raw JSON Schema document into pi's Schema tree.
func jsonSchemaToPi(raw json.RawMessage) *ai.Schema {
	if len(raw) == 0 {
		return nil
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	return schemaFromMap(doc)
}

// schemaFromMap maps the JSON Schema keywords pi's Schema models, and ignores
// the rest. A keyword with no field on ai.Schema is dropped rather than
// approximated, because this schema reaches a provider as a hint.
func schemaFromMap(doc map[string]any) *ai.Schema {
	if doc == nil {
		return nil
	}
	s := &ai.Schema{}
	if t, ok := doc["type"].(string); ok {
		s.Type = t
	}
	if d, ok := doc["description"].(string); ok {
		s.Description = d
	}
	if props, ok := doc["properties"].(map[string]any); ok {
		s.Properties = make(map[string]*ai.Schema, len(props))
		for name, v := range props {
			if pm, ok := v.(map[string]any); ok {
				s.Properties[name] = schemaFromMap(pm)
			}
		}
	}
	if req, ok := doc["required"].([]any); ok {
		s.Required = make([]string, 0, len(req))
		for _, r := range req {
			if rs, ok := r.(string); ok {
				s.Required = append(s.Required, rs)
			}
		}
	}
	if items, ok := doc["items"].(map[string]any); ok {
		s.Items = schemaFromMap(items)
	}
	if en, ok := doc["enum"].([]any); ok {
		s.Enum = en
	}
	if d, ok := doc["default"]; ok {
		s.Default = d
	}
	if ap, ok := doc["additionalProperties"].(bool); ok {
		b := ap
		s.AdditionalAllowed = &b
	}
	return s
}

// assistantTurnResponse renders one pi assistant turn in the shape the
// transcript recorder already understands, so the loop's tool calls are
// recorded exactly the way the outline's structured reply is.
func assistantTurnResponse(am ai.AssistantMessage) provider.ProviderResponse {
	var resp provider.ProviderResponse
	for _, c := range am.Content {
		switch c := c.(type) {
		case ai.TextContent:
			resp.Text += c.Text
		case ai.ToolCall:
			args, err := json.Marshal(c.Arguments)
			if err != nil {
				args = []byte("{}")
			}
			resp.ToolCalls = append(resp.ToolCalls, provider.ToolCall{
				ID:       c.ID,
				Function: provider.ToolCallFunction{Name: c.Name, Arguments: string(args)},
			})
		}
	}
	return resp
}

func textFromContent(cl ai.ContentList) string {
	var b strings.Builder
	for _, c := range cl {
		if tc, ok := c.(ai.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
