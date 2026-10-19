# ADR 0008: The tutor sees the expected result, never the reference query

- **Status:** accepted
- **Date:** 2026-10-18
- **Deciders:** project owner + agent session
- **Question:** when the tutor helps a learner, may the model see the exercise's
  answer, and if so which part of it?

Settles **Ask Tutor**, and amends ADR 0005's "What the model may see".

## Context

One rule has run through the tutoring code since M4: the model never sees the
reference solution, and a reply that contains one is discarded
(`backend/internal/ai/review.go`). The learner is the only actor who may accept
an answer, and nothing the model says changes a status.

The owner asked the tutor to help with the exercise in front of the learner. That
question has two different answers hiding in the word "answer":

- The **expected result** is the rows and columns the correct query returns. A
  teacher who knows the answer to the exercise is still teaching.
- The **reference query** is the query that produces those rows. A teacher who
  hands it over has done the exercise for the learner.

Conflating the two would either starve the tutor of the context it needs or hand
the learner the work. The first verdict also stays deterministic: the evaluator
compares results, never SQL text.

Three facts shaped the decision:

1. The client already sends `reference_solution` to `POST /answers/evaluate`
   (`contracts/exercise.schema.json`). The gateway can run it to compute the
   oracle without trusting any row the client claims.
2. `guardSolutions` compares a reply against a *stored* artifact, so an authored
   exercise that was never stored got no guard at all.
3. The pipeline assembles the context server-side. The client sends facts
   (routing, the exercise reference, the learner's SQL, the turns), and the
   gateway runs the SQL and the reference itself.

## Decision

1. **The tutor may see the expected result.** The gateway runs the reference in
   the resolved environment and keeps the rows, discarding the query. A stored
   course exercise already carries the engine-measured rows from generation.
2. **The expected rows appear only after an attempt.** Before the learner runs,
   the context carries the contract columns and a row count, not rows. After any
   run, it carries up to 50 rows. A learner who opens the drawer first is still
   doing the exercise.
3. **The reference query never reaches the provider.** The gateway keeps the
   request's own reference for two things only: to compute the oracle, and to arm
   `guardSolutions`. It travels on `TutorRequest.Guards`, a `json:"-"` field, and
   never enters the rendered prompt. The system prompt keeps its ban on writing
   the full solution, and `reveals_solution` stays false.
4. **The reference arms the guard for authored exercises.** The request's own
   reference now closes the gap that an authored, unstored exercise had no guard.
5. **The prompt says so plainly.** "The expected result is the shape and values
   the answer must produce. Do not write the query that produces it."

The controls are advisory at the model and structural at the gateway. The
gateway fixes `Kind` and `System`, passes no tools, and refuses a reply that
contains a guard. A determined model can still write a close query from the rows.
The learner passes by their own result, and the reply is one hint, not a stored
artifact.

## Consequences

- The drawer can show what the tutor saw: the context summary lists the dataset
  tables, the learner's run state, and whether the expected result is in view.
  It never guesses.
- A reply that echoes the reference is refused with `AI_INVALID_ARTIFACT`, and
  the refusal does not echo it back.
- The prompt carries untrusted text (SQL comments, data) inside explicit fences,
  with the fence markers stripped from the content. Kind and System stay fixed by
  the gateway, so injection buys a misleading explanation at most.
- The reference query still crosses the wire on `/answers/evaluate`, where it
  always did. The tutor route does not add a new exposure of it.

## Alternatives considered

- **No expected result at all.** Keeps the strictest reading, but leaves the
  tutor unable to answer "why is my result wrong". Rejected.
- **Send the oracle rows to the client so the drawer can compare.** Makes the
  client the source of result rows, which is the fabrication boundary
  `/answers/evaluate` already refuses. Rejected.
- **Give the tutor a `run_sql` tool.** Turns one completion into an agent loop
  with an injection surface and a second budget, before any measured need.
  Deferred, not forgotten.
