package armor_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/armor"
	"github.com/tkdtaylor/agent-builder/internal/ingestion"
)

var _ ingestion.Guard = armor.Guard{}

func TestAdapterInvokesArmorBehindGuardSeam(t *testing.T) {
	content := mustContent(t, "tc001")
	runner := &recordingRunner{
		response: armor.Response{Decision: "allow", Metadata: map[string]string{"request_seen": "yes"}},
	}
	guard := armor.NewGuard(armor.Config{Runner: runner})

	decision, err := guard.DecideContent(context.Background(), content)
	if err != nil {
		t.Fatalf("TC-001 DecideContent error = %v", err)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("TC-001 runner requests = %d, want 1", len(runner.requests))
	}
	request := runner.requests[0]
	if request.CandidateID != string(content.ID) || request.Kind != string(ingestion.CandidateKindContent) {
		t.Fatalf("TC-001 request correlation = (%q, %q), want (%q, %q)", request.CandidateID, request.Kind, content.ID, ingestion.CandidateKindContent)
	}
	if request.Content != string(content.Content) || request.SourceURI != content.SourceURI || request.MediaType != content.MediaType {
		t.Fatalf("TC-001 request content fields not preserved: %+v", request)
	}
	if request.Provenance["task_id"] != "025" || request.Provenance["executor"] != "test" {
		t.Fatalf("TC-001 request provenance = %+v, want task/executor metadata", request.Provenance)
	}
	if decision.CandidateID != content.ID || decision.Kind != ingestion.CandidateKindContent {
		t.Fatalf("TC-001 decision correlation = (%q, %q), want (%q, %q)", decision.CandidateID, decision.Kind, content.ID, ingestion.CandidateKindContent)
	}
	if decision.Outcome != ingestion.DecisionAllow {
		t.Fatalf("TC-002 decision outcome = %q, want allow", decision.Outcome)
	}
}

func TestBenignArmorResultMapsToAllow(t *testing.T) {
	toolCall := mustToolCall(t, "tc002")
	runner := &recordingRunner{
		response: armor.Response{
			Decision: "clean",
			Warnings: []string{"low confidence", "low confidence", " informational "},
			Metadata: map[string]string{"armor_version": "scripted"},
		},
	}
	guard := armor.NewGuard(armor.Config{Runner: runner})

	decision, err := guard.DecideToolCall(context.Background(), toolCall)
	if err != nil {
		t.Fatalf("TC-002 DecideToolCall error = %v", err)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("TC-001 runner requests = %d, want 1", len(runner.requests))
	}
	request := runner.requests[0]
	if request.CandidateID != string(toolCall.ID) || request.Kind != string(ingestion.CandidateKindToolCall) {
		t.Fatalf("TC-001 tool request correlation = (%q, %q), want (%q, %q)", request.CandidateID, request.Kind, toolCall.ID, ingestion.CandidateKindToolCall)
	}
	if request.ToolName != toolCall.ToolName || string(request.Arguments) != string(toolCall.Arguments) || request.TargetURI != toolCall.TargetURI {
		t.Fatalf("TC-001 tool request fields not preserved: %+v", request)
	}
	if decision.Outcome != ingestion.DecisionAllow {
		t.Fatalf("TC-002 decision outcome = %q, want allow", decision.Outcome)
	}
	if decision.Reason != "" {
		t.Fatalf("TC-002 decision reason = %q, want empty allow reason", decision.Reason)
	}
	if decision.Metadata["armor_version"] != "scripted" {
		t.Fatalf("TC-002 metadata armor_version = %q, want scripted", decision.Metadata["armor_version"])
	}
	if decision.Metadata["warnings"] != "informational,low confidence" {
		t.Fatalf("TC-002 warnings metadata = %q, want deterministic warning list", decision.Metadata["warnings"])
	}
}

func TestArmorFindingsMapToBlockOrQuarantine(t *testing.T) {
	tests := []struct {
		name      string
		response  armor.Response
		want      ingestion.DecisionOutcome
		wantCat   string
		wantSev   string
		wantCause string
	}{
		{
			name: "injection blocked",
			response: armor.Response{
				Decision: "flag",
				Findings: []armor.Finding{{Category: "prompt-injection", Severity: "high", Message: "ignore previous instructions"}},
			},
			want:      ingestion.DecisionBlock,
			wantCat:   "prompt-injection",
			wantSev:   "high",
			wantCause: "ignore previous instructions",
		},
		{
			name: "exfiltration blocked",
			response: armor.Response{
				Decision: "block",
				Reason:   "secret exfiltration attempt",
				Findings: []armor.Finding{{Category: "exfiltration", Severity: "critical"}},
			},
			want:      ingestion.DecisionBlock,
			wantCat:   "exfiltration",
			wantSev:   "critical",
			wantCause: "secret exfiltration attempt",
		},
		{
			name: "unsafe tool quarantined",
			response: armor.Response{
				Decision: "quarantine",
				Findings: []armor.Finding{
					{Category: "unsafe-tool-call", Severity: "medium", Message: "dangerous target"},
					{Category: "prompt-injection", Severity: "high", Message: "tool bait"},
				},
			},
			want:      ingestion.DecisionQuarantine,
			wantCat:   "prompt-injection,unsafe-tool-call",
			wantSev:   "high,medium",
			wantCause: "dangerous target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := mustContent(t, tt.name)
			guard := armor.NewGuard(armor.Config{Runner: &recordingRunner{response: tt.response}})

			decision, err := guard.DecideContent(context.Background(), content)
			if err != nil {
				t.Fatalf("TC-003 DecideContent error = %v", err)
			}
			if decision.Outcome != tt.want {
				t.Fatalf("TC-003 outcome = %q, want %q", decision.Outcome, tt.want)
			}
			if !strings.Contains(decision.Reason, tt.wantCause) {
				t.Fatalf("TC-003 reason = %q, want cause containing %q", decision.Reason, tt.wantCause)
			}
			if decision.Metadata["finding_categories"] != tt.wantCat {
				t.Fatalf("TC-003 categories = %q, want %q", decision.Metadata["finding_categories"], tt.wantCat)
			}
			if decision.Metadata["finding_severities"] != tt.wantSev {
				t.Fatalf("TC-003 severities = %q, want %q", decision.Metadata["finding_severities"], tt.wantSev)
			}
		})
	}
}

func TestArmorInvocationFailuresFailClosed(t *testing.T) {
	content := mustContent(t, "tc004")
	tests := []struct {
		name   string
		config armor.Config
	}{
		{name: "missing runner and command", config: armor.Config{}},
		{name: "runner error", config: armor.Config{Runner: &recordingRunner{err: errors.New("armor unavailable")}}},
		{name: "timeout", config: armor.Config{Runner: &blockingRunner{}, Timeout: time.Nanosecond}},
		{name: "malformed output", config: armor.Config{Runner: &recordingRunner{response: armor.Response{Decision: "surprise"}}}},
		{name: "armor error response", config: armor.Config{Runner: &recordingRunner{response: armor.Response{Decision: "error", Reason: "policy service error"}}}},
		{name: "non-zero process exit", config: armor.Config{Command: writeFakeArmorCommand(t, `{"decision":"allow"}`, 7, "boom")}},
		{name: "malformed process json", config: armor.Config{Command: writeFakeArmorCommand(t, `not json`, 0, "")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard := armor.NewGuard(tt.config)
			decision, err := guard.DecideContent(context.Background(), content)
			if err != nil {
				t.Fatalf("TC-004 DecideContent error = %v, want fail-closed decision without adapter error", err)
			}
			if decision.Outcome != ingestion.DecisionBlock {
				t.Fatalf("TC-004 outcome = %q, want fail-closed block", decision.Outcome)
			}
			if !strings.Contains(decision.Reason, "fail closed") {
				t.Fatalf("TC-004 reason = %q, want fail-closed reason", decision.Reason)
			}
		})
	}
}

func TestArmorSourceRemainsExternal(t *testing.T) {
	for _, path := range []string{
		"armor",
		"vendor/armor",
		"third_party/armor",
		"internal/armor/vendor",
	} {
		if _, err := os.Stat(filepath.Join(repoRoot(t), path)); err == nil {
			t.Fatalf("TC-005 external armor source path exists in agent-builder: %s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("TC-005 stat %s: %v", path, err)
		}
	}
}

type recordingRunner struct {
	requests []armor.Request
	response armor.Response
	err      error
}

func (r *recordingRunner) Run(ctx context.Context, request armor.Request) (armor.Response, error) {
	r.requests = append(r.requests, cloneRequest(request))
	return r.response, r.err
}

type blockingRunner struct{}

func (blockingRunner) Run(ctx context.Context, request armor.Request) (armor.Response, error) {
	<-ctx.Done()
	return armor.Response{}, ctx.Err()
}

func mustContent(t *testing.T, id string) ingestion.ContentCandidate {
	t.Helper()
	candidate, err := ingestion.NewContentCandidate(ingestion.ContentInput{
		ID:        ingestion.CandidateID(id),
		Content:   []byte("benign content " + id),
		SourceURI: "https://example.test/" + id,
		MediaType: "text/plain",
		Provenance: ingestion.Provenance{
			TaskID:   "025",
			Executor: "test",
		},
	})
	if err != nil {
		t.Fatalf("create content candidate: %v", err)
	}
	return candidate
}

func mustToolCall(t *testing.T, id string) ingestion.ToolCallCandidate {
	t.Helper()
	candidate, err := ingestion.NewToolCallCandidate(ingestion.ToolCallInput{
		ID:        ingestion.CandidateID(id),
		ToolName:  "web.fetch",
		Arguments: json.RawMessage(`{"url":"https://example.test"}`),
		TargetURI: "https://example.test",
		Provenance: ingestion.Provenance{
			TaskID:   "025",
			Executor: "test",
		},
	})
	if err != nil {
		t.Fatalf("create tool-call candidate: %v", err)
	}
	return candidate
}

func cloneRequest(request armor.Request) armor.Request {
	request.Arguments = append([]byte(nil), request.Arguments...)
	if len(request.Provenance) > 0 {
		provenance := make(map[string]string, len(request.Provenance))
		for key, value := range request.Provenance {
			provenance[key] = value
		}
		request.Provenance = provenance
	}
	return request
}

func writeFakeArmorCommand(t *testing.T, stdout string, exitCode int, stderr string) []string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "fake-armor")
	script := "#!/bin/sh\ncat >/dev/null\n"
	if stderr != "" {
		script += "printf '%s\\n' " + shellQuote(stderr) + " >&2\n"
	}
	if stdout != "" {
		script += "printf '%s\\n' " + shellQuote(stdout) + "\n"
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake armor command: %v", err)
	}
	return []string{path}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working dir: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}
