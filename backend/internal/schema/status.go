package schema

/* ---------- stored status and state vocabulary ---------- */

// StageStates. Done, failed and skipped are final: they are what a finished run
// reports, and the outcome envelope's schema allows nothing else. Queued and
// running exist only in a live view of a run in flight (jobs.go), where "this
// has not started" and "this is happening now" are the two facts a poller
// needs and neither is a verdict about the stage.
const (
	StateQueued  = "queued"
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
	StateSkipped = "skipped"
)

// OutcomeStatus values. Only StatusActive puts an artifact in front of learners.
const (
	StatusActive      = "active"
	StatusRejected    = "rejected"
	StatusUnavailable = "unavailable"
	StatusFailed      = "failed"
	// StatusBuilding is the stored state of a course that is still being
	// written, one chapter at a time. It is a status of a stored record, never
	// of a generation outcome: a run that returns is active, rejected, failed
	// or awaiting approval. It exists so GET /courses/{id} can answer with the
	// chapters that have already verified while later ones are still being
	// generated.
	StatusBuilding = "building"
	// StatusAwaitingApproval is the outcome of a run that was asked to pause
	// after its outline (GenerationRequest.RequireApproval). It is a
	// *non-terminal* stop: the run ends here, but the stored course it names is
	// a live thing, and POST /courses/{id}/build continues it into the chapter
	// phase. `active` stays the only status that puts a course in front of
	// learners, so nothing appears in a learner's library as a result of this
	// status alone — the building row the outline stored is all that exists.
	StatusAwaitingApproval = "awaiting_approval"
	// StatusDiscarded is a job-view status, not an outcome one. It says the
	// course a paused run was waiting on was removed, so a poller is told why
	// the run it was watching will never continue. The run's own transcript
	// still records the status the run ended with — it really did stop at
	// awaiting_approval; the discard happened afterwards, to the record.
	StatusDiscarded = "discarded"
)
