package executorharness

import "github.com/tkdtaylor/agent-builder/internal/ingestion"

// TraceStage names a producer-consumer checkpoint in the executor harness.
type TraceStage string

const (
	TraceContentCandidateProduced TraceStage = "content-candidate-produced"
	TraceContentBrokerReviewed    TraceStage = "content-broker-reviewed"
	TraceContentReleased          TraceStage = "content-released"
	TraceToolCandidateProduced    TraceStage = "tool-candidate-produced"
	TraceToolBrokerReviewed       TraceStage = "tool-broker-reviewed"
	TraceToolReleased             TraceStage = "tool-released"
	TraceToolExecuted             TraceStage = "tool-executed"
)

// TraceEvent records one live producer-consumer checkpoint.
type TraceEvent struct {
	Stage       TraceStage
	CandidateID ingestion.CandidateID
	Kind        ingestion.CandidateKind
	Outcome     ingestion.DecisionOutcome
}

// TraceRecorder consumes harness trace events.
type TraceRecorder interface {
	RecordTrace(TraceEvent)
}
