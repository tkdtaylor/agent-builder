// Package runtime assembles the concrete Phase 0 run pipeline for the CLI.
package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/gate"
	agentloop "github.com/tkdtaylor/agent-builder/internal/loop"
	branchpub "github.com/tkdtaylor/agent-builder/internal/publisher"
	"github.com/tkdtaylor/agent-builder/internal/sandbox"
	"github.com/tkdtaylor/agent-builder/internal/sandbox/podman"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

const (
	EnvTaskRoot        = "AGENT_BUILDER_TASK_ROOT"
	EnvWorktree        = "AGENT_BUILDER_WORKTREE"
	EnvClaudeCLI       = "AGENT_BUILDER_CLAUDE_CLI"
	EnvExecBoxLauncher = "AGENT_BUILDER_EXEC_BOX_LAUNCHER"
	EnvRunRecord       = "AGENT_BUILDER_RUN_RECORD"
	EnvAuditRecord     = "AGENT_BUILDER_AUDIT_RECORD"
	EnvAuditBin        = "AGENT_BUILDER_AUDIT_BIN"
	EnvRunTimeout      = "AGENT_BUILDER_RUN_TIMEOUT"
	EnvMaxAttempts     = "AGENT_BUILDER_MAX_ATTEMPTS"
	EnvPublishRemote   = "AGENT_BUILDER_PUBLISH_REMOTE"
	EnvGitCLI          = "AGENT_BUILDER_GIT_CLI"
	EnvGitHubCLI       = "AGENT_BUILDER_GH_CLI"
	EnvGitToken        = "AGENT_BUILDER_GIT_TOKEN"
	EnvGitHubToken     = "AGENT_BUILDER_GITHUB_TOKEN"

	// EnvSandboxRuntime is the removed Phase 0 srt selector. It is retained only
	// to detect and reject a stale value loudly (ADR 021, decision 2).
	EnvSandboxRuntime = "AGENT_BUILDER_SANDBOX_RUNTIME"

	// defaultExecBoxLauncher is the standard Podman execution-box launcher path.
	defaultExecBoxLauncher = "containment/execution-box/run.sh"
)

// Config is the explicit runtime configuration used by agent-builder run.
type Config struct {
	TaskRoot        string
	Worktree        string
	ClaudeCLI       string
	ClaudeToken     string
	ExecBoxLauncher string
	RunRecordPath   string
	AuditRecordPath string
	AuditBin        string
	RunTimeout      time.Duration
	MaxAttempts     int
	PublishRemote   string
	GitCLI          string
	GitHubCLI       string
	GitToken        string
	GitHubToken     string
}

// RunFromEnv builds and runs one configured Phase 0 pipeline from environment
// variables. The optional writer receives a short user-visible run summary.
func RunFromEnv(stdout io.Writer) error {
	config, err := ConfigFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	return Run(config, stdout)
}

// ConfigFromEnv reads the explicit run configuration contract from getenv.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	// The rented srt selector was removed by ADR 021. A stale non-empty value
	// must fail loudly rather than be silently ignored (decision 2).
	if strings.TrimSpace(getenv(EnvSandboxRuntime)) != "" {
		return Config{}, fmt.Errorf("run config: %s was removed by the Podman containment swap (ADR 021); unset it — containment now runs through %s", EnvSandboxRuntime, defaultExecBoxLauncher)
	}

	config := Config{
		TaskRoot:        cleanPath(getenv(EnvTaskRoot)),
		Worktree:        cleanPath(getenv(EnvWorktree)),
		ClaudeCLI:       strings.TrimSpace(getenv(EnvClaudeCLI)),
		ClaudeToken:     getenv(executor.ClaudeCLIAuthEnv),
		ExecBoxLauncher: cleanPath(getenv(EnvExecBoxLauncher)),
		RunRecordPath:   cleanPath(getenv(EnvRunRecord)),
		AuditRecordPath: cleanPath(getenv(EnvAuditRecord)),
		AuditBin:        strings.TrimSpace(getenv(EnvAuditBin)),
		PublishRemote:   strings.TrimSpace(getenv(EnvPublishRemote)),
		GitCLI:          strings.TrimSpace(getenv(EnvGitCLI)),
		GitHubCLI:       strings.TrimSpace(getenv(EnvGitHubCLI)),
		GitToken:        getenv(EnvGitToken),
		GitHubToken:     getenv(EnvGitHubToken),
	}
	if config.ClaudeCLI == "" {
		config.ClaudeCLI = "claude"
	}
	if config.ExecBoxLauncher == "" {
		config.ExecBoxLauncher = defaultExecBoxLauncher
	}
	if config.GitCLI == "" {
		config.GitCLI = "git"
	}
	if config.GitHubCLI == "" {
		config.GitHubCLI = "gh"
	}

	if config.TaskRoot == "" {
		return Config{}, missingConfig(EnvTaskRoot)
	}
	if config.Worktree == "" {
		return Config{}, missingConfig(EnvWorktree)
	}
	if strings.TrimSpace(config.ClaudeToken) == "" {
		return Config{}, missingConfig(executor.ClaudeCLIAuthEnv)
	}
	if config.PublishRemote == "" {
		return Config{}, missingConfig(EnvPublishRemote)
	}

	timeoutRaw := strings.TrimSpace(getenv(EnvRunTimeout))
	if timeoutRaw == "" {
		return Config{}, missingConfig(EnvRunTimeout)
	}
	timeout, err := time.ParseDuration(timeoutRaw)
	if err != nil {
		return Config{}, fmt.Errorf("run config: invalid %s %q: %w", EnvRunTimeout, timeoutRaw, err)
	}
	config.RunTimeout = timeout

	attemptsRaw := strings.TrimSpace(getenv(EnvMaxAttempts))
	if attemptsRaw == "" {
		return Config{}, missingConfig(EnvMaxAttempts)
	}
	attempts, err := strconv.Atoi(attemptsRaw)
	if err != nil {
		return Config{}, fmt.Errorf("run config: invalid %s %q: %w", EnvMaxAttempts, attemptsRaw, err)
	}
	config.MaxAttempts = attempts

	return config, nil
}

// Run dispatches at most one ready task through the concrete Phase 0 seams.
func Run(config Config, stdout io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if err := validatePaths(config); err != nil {
		return err
	}

	source := tasksource.New(os.DirFS(config.TaskRoot), tasksource.DefaultRoadmapPath, tasksource.DefaultTaskDirs...)
	task, ok, err := source.Next()
	if err != nil {
		return fmt.Errorf("run: pick task: %w", err)
	}
	if !ok {
		_, _ = fmt.Fprintln(stdout, "run idle: no ready task")
		return nil
	}

	verifier, err := newProductionGate()
	if err != nil {
		return err
	}
	policy, err := agentloop.NewRetryPolicy(config.MaxAttempts, agentloop.BootstrapEscalationHook)
	if err != nil {
		return fmt.Errorf("run config: invalid %s: %w", EnvMaxAttempts, err)
	}

	exec := executor.NewClaudeCLI(executor.ClaudeCLIConfig{
		CLIPath:   config.ClaudeCLI,
		Worktree:  config.Worktree,
		AuthToken: config.ClaudeToken,
	})
	runner := podman.NewWithLauncher(config.ExecBoxLauncher)
	box := sandboxBox{
		runner:   runner,
		worktree: config.Worktree,
		launcher: config.ExecBoxLauncher,
		limits: sandbox.Limits{
			WallClockTimeout: config.RunTimeout,
		},
	}
	inBox := retryingInBoxLoop{
		executor:     exec,
		gate:         verifier,
		worktree:     config.Worktree,
		launcher:     config.ExecBoxLauncher,
		statusWriter: tasksource.NewStatusWriter(config.TaskRoot, tasksource.DefaultTaskDirs...),
		policy:       policy,
		publisher: branchpub.NewGitHubCLI(branchpub.GitHubCLIConfig{
			GitPath:     config.GitCLI,
			GHPath:      config.GitHubCLI,
			Worktree:    config.Worktree,
			Remote:      config.PublishRemote,
			GitToken:    config.GitToken,
			GitHubToken: config.GitHubToken,
		}),
		publishRemote: config.PublishRemote,
		publishSecrets: []string{
			config.GitToken,
			config.GitHubToken,
		},
	}

	options := []supervisor.Option{
		supervisor.WithTask(task),
		supervisor.WithContainmentBox(box),
		supervisor.WithInBoxLoop(inBox),
		supervisor.WithRunTimeout(config.RunTimeout),
	}
	if config.RunRecordPath != "" {
		options = append(options, supervisor.WithRunRecordPath(config.RunRecordPath))
	}
	// Optional typed audit chain (task 041). When AGENT_BUILDER_AUDIT_RECORD is
	// set, the audit-trail binary must resolve and the path must be writable
	// BEFORE dispatch — auditing is never silently skipped when configured.
	if config.AuditRecordPath != "" {
		sink, err := newBlockSink(config)
		if err != nil {
			return err
		}
		options = append(options, supervisor.WithSink(sink))
	}

	if err := supervisor.New(options...).Run(); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "run completed: task %s\n", task.ID)
	return nil
}

// newBlockSink resolves the audit-trail binary and verifies the chain logfile
// path is writable, then constructs the production audit.BlockSink. It fails
// fast with a clear configuration error when the binary cannot be resolved or
// the path cannot be written — auditing is never silently skipped once
// AGENT_BUILDER_AUDIT_RECORD is configured. The supervisor depends only on the
// audit.Sink interface; BlockSink reaches the block over os/exec, so no block
// package enters the supervisor import graph (F-003 holds).
func newBlockSink(config Config) (audit.Sink, error) {
	binPath, err := resolveAuditBin(config.AuditBin)
	if err != nil {
		return nil, err
	}
	if err := requireWritable(config.AuditRecordPath); err != nil {
		return nil, err
	}
	return audit.NewBlockSink(binPath, config.AuditRecordPath), nil
}

// resolveAuditBin resolves the audit-trail binary path: an explicit
// AGENT_BUILDER_AUDIT_BIN value (validated executable) takes precedence,
// otherwise the bare name "audit-trail" is looked up on $PATH. An unresolvable
// binary is a hard configuration error.
func resolveAuditBin(configured string) (string, error) {
	if configured != "" {
		resolved, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("run config: %s %q is not an executable audit-trail binary: %w", EnvAuditBin, configured, err)
		}
		return resolved, nil
	}
	resolved, err := exec.LookPath("audit-trail")
	if err != nil {
		return "", fmt.Errorf("run config: %s is set but no audit-trail binary resolves (set %s or add audit-trail to PATH): %w", EnvAuditRecord, EnvAuditBin, err)
	}
	return resolved, nil
}

// requireWritable confirms the audit chain logfile path can be created/appended
// before dispatch. The block owns the chain format and appends to this path; we
// only verify the host side can write it so a misconfigured path fails loudly
// up front rather than mid-run.
func requireWritable(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) //nolint:gosec // operator-supplied audit chain path
	if err != nil {
		return fmt.Errorf("run config: %s %q is not writable: %w", EnvAuditRecord, path, err)
	}
	return file.Close()
}

func validatePaths(config Config) error {
	if err := requireDir(EnvTaskRoot, config.TaskRoot); err != nil {
		return err
	}
	if err := requireDir(EnvWorktree, config.Worktree); err != nil {
		return err
	}
	return nil
}

func requireDir(name, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("run config: %s %q is not usable: %w", name, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("run config: %s %q is not a directory", name, path)
	}
	return nil
}

func missingConfig(name string) error {
	return fmt.Errorf("run config: missing %s", name)
}

func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

type sandboxBox struct {
	runner   sandbox.Runner
	worktree string
	launcher string
	limits   sandbox.Limits
}

func (b sandboxBox) Create(task supervisor.Task) (supervisor.BoxHandle, error) {
	if b.runner == nil {
		return supervisor.BoxHandle{}, fmt.Errorf("run config: missing sandbox runner")
	}
	// The execution-box image is ENTRYPOINT ["/bin/sh"] (ADR 032), so the command
	// is passed to /bin/sh as its arguments: a bare ["/bin/true"] becomes `sh /bin/true`,
	// which makes the shell read the ELF binary as a script ("ELF: not found", exit 2).
	// Use an sh -c form so the box-liveness probe runs `sh -c true` and exits 0.
	_, exitCode, err := b.runner.Run(sandbox.Request{
		Command:  []string{"-c", "true"},
		Worktree: b.worktree,
		Limits:   b.limits,
	})
	if err != nil {
		return supervisor.BoxHandle{}, err
	}
	if exitCode != 0 {
		return supervisor.BoxHandle{}, fmt.Errorf("sandbox: create probe exited %d", exitCode)
	}
	return supervisor.BoxHandle{
		ID:       "sandbox-" + strings.TrimSpace(task.ID),
		Worktree: b.worktree,
	}, nil
}

func (b sandboxBox) Kill(supervisor.BoxHandle) error {
	return nil
}

func (b sandboxBox) Teardown(supervisor.BoxHandle) error {
	return nil
}

type retryingInBoxLoop struct {
	executor       supervisor.Executor
	gate           supervisor.Gate
	worktree       string
	launcher       string
	statusWriter   agentloop.StatusWriter
	policy         agentloop.RetryPolicy
	publisher      branchpub.Publisher
	publishRemote  string
	publishSecrets []string
}

func (l retryingInBoxLoop) RunInside(handle supervisor.BoxHandle, task supervisor.Task, streams supervisor.RunStreams) error {
	runID := auditRunID(task, handle)
	writeCommand(streams, "containment=podman launcher=%s", l.launcher)
	emitAudit(streams, audit.AuditEvent{
		Action: audit.ActionContainment, RunID: runID, TaskID: task.ID,
		Detail: audit.EventDetail{Launcher: l.launcher},
	})
	writeCommand(streams, "pick task %s", task.ID)
	emitAudit(streams, audit.AuditEvent{Action: audit.ActionPick, RunID: runID, TaskID: task.ID})
	writeStdout(streams, "task %s selected\n", task.ID)

	runner, err := agentloop.NewRetryingLoop(singleTaskSource{task: task}, l.executor, l.gate, l.worktree, l.statusWriter, l.policy)
	if err != nil {
		return err
	}
	outcome, err := runner.RunOnce()
	if err != nil {
		return err
	}
	for _, state := range outcome.LastOutcome.Trace {
		switch state {
		case agentloop.StateAttempt:
			writeCommand(streams, "attempt task %s attempt=%d", task.ID, outcome.Attempts)
			emitAudit(streams, audit.AuditEvent{
				Action: audit.ActionAttempt, RunID: runID, TaskID: task.ID,
				Detail: audit.EventDetail{Attempt: outcome.Attempts},
			})
		case agentloop.StateVerify:
			writeCommand(streams, "verify worktree %s", l.worktree)
			emitAudit(streams, audit.AuditEvent{
				Action: audit.ActionVerify, RunID: runID, TaskID: task.ID,
				Verdict: auditVerdict(outcome.LastOutcome.Verdict),
			})
		}
	}

	switch outcome.Kind {
	case agentloop.RetryOutcomeDone:
		writeStdout(streams, "executor attempt completed: branch=%s\n", outcome.Branch)
		writeStdout(streams, "gate passed: %s\n", summarizeVerdict(outcome.LastOutcome.Verdict))
		writeCommand(streams, "publish branch %s remote=%s", outcome.Branch, l.redact(l.publishRemote))
		emitAudit(streams, audit.AuditEvent{
			Action: audit.ActionPublish, RunID: runID, TaskID: task.ID,
			Detail: audit.EventDetail{Branch: outcome.Branch, Remote: l.redact(l.publishRemote)},
		})
		if l.publisher == nil {
			return fmt.Errorf("run: publish task %s: missing publisher", task.ID)
		}
		result, err := l.publisher.Publish(context.Background(), branchpub.Request{
			Task:     task,
			Worktree: l.worktree,
			Branch:   outcome.Branch,
			Remote:   l.publishRemote,
		})
		if err != nil {
			message := l.redact(err.Error())
			writeStderr(streams, "publication failed: %s\n", message)
			return fmt.Errorf("run: publish task %s: %s", task.ID, message)
		}
		prArtifact := result.PRURL
		if prArtifact == "" {
			prArtifact = result.PRID
		}
		writeStdout(streams, "publication recorded: branch=%s pr=%s\n", outcome.Branch, l.redact(prArtifact))
		writeCommand(streams, "finish task %s outcome=completed branch=%s pr=%s", task.ID, outcome.Branch, l.redact(prArtifact))
		emitAudit(streams, audit.AuditEvent{
			Action: audit.ActionFinish, RunID: runID, TaskID: task.ID, Outcome: audit.OutcomeCompleted,
			Detail: audit.EventDetail{Branch: outcome.Branch},
		})
		return nil
	case agentloop.RetryOutcomeEscalated:
		writeFailureEvidence(streams, outcome.LastOutcome)
		writeStderr(streams, "task %s escalated after %d attempts\n", task.ID, outcome.Attempts)
		emitAudit(streams, audit.AuditEvent{
			Action: audit.ActionEscalate, RunID: runID, TaskID: task.ID,
			Detail: audit.EventDetail{Attempt: outcome.Attempts},
		})
		emitAudit(streams, audit.AuditEvent{
			Action: audit.ActionFinish, RunID: runID, TaskID: task.ID, Outcome: audit.OutcomeFailed,
		})
		return fmt.Errorf("run: task %s escalated after %d attempts", task.ID, outcome.Attempts)
	case agentloop.RetryOutcomeIdle:
		writeStdout(streams, "no ready task\n")
		return nil
	default:
		return fmt.Errorf("run: unexpected retry outcome %q", outcome.Kind)
	}
}

func (l retryingInBoxLoop) redact(text string) string {
	return branchpub.Redact(text, l.publishSecrets)
}

type singleTaskSource struct {
	task supervisor.Task
}

func (s singleTaskSource) Next() (supervisor.Task, bool, error) {
	return s.task, true, nil
}

func summarizeVerdict(verdict gate.Verdict) string {
	if len(verdict.Results) == 0 {
		if verdict.OK {
			return "no steps"
		}
		return "failed before steps"
	}
	parts := make([]string, 0, len(verdict.Results))
	for _, result := range verdict.Results {
		status := "FAIL"
		if result.OK {
			status = "PASS"
		}
		parts = append(parts, status+" "+result.Name)
	}
	return strings.Join(parts, "; ")
}

func writeFailureEvidence(streams supervisor.RunStreams, outcome agentloop.Outcome) {
	switch outcome.Failure.Reason {
	case agentloop.FailureExecutorError:
		if outcome.Failure.Err != nil {
			writeStderr(streams, "executor error: %v\n", outcome.Failure.Err)
		}
	case agentloop.FailureExecutorIncomplete:
		writeStderr(streams, "executor incomplete: branch=%s\n", outcome.Branch)
	case agentloop.FailureGate:
		writeStderr(streams, "gate failed: %s\n", summarizeVerdict(outcome.Verdict))
		for _, result := range outcome.Verdict.Results {
			if result.OK {
				continue
			}
			output := strings.TrimSpace(result.Output)
			if output == "" {
				output = "no output"
			}
			writeStderr(streams, "gate step %s failed: %s\n", result.Name, output)
			return
		}
	}
}

func writeCommand(streams supervisor.RunStreams, format string, args ...any) {
	if streams.Command == nil {
		return
	}
	_, _ = fmt.Fprintf(streams.Command, format, args...)
}

// emitAudit projects one typed action event through the optional audit Sink. It
// is a no-op when no Sink is configured (streams.Audit == nil), so a run without
// AGENT_BUILDER_AUDIT_RECORD behaves exactly as before. Only typed action events
// flow here — raw stdout/stderr stay in the RunRecord and never reach the Sink.
//
// Append errors are intentionally swallowed for the in-loop projection: the
// chain is a parallel durable artifact, not the run's control flow, and the
// block-severity integrity gate (VerifyChain) is what surfaces a corrupt chain.
// Misconfiguration (unresolvable binary / unwritable path) is caught up front by
// newBlockSink before dispatch, so a live Append failure here is a block-side
// fault, not a silent skip of a configured audit.
func emitAudit(streams supervisor.RunStreams, ev audit.AuditEvent) {
	if streams.Audit == nil {
		return
	}
	_ = streams.Audit.Append(ev)
}

// auditRunID derives the run correlation id used in audit events, matching the
// RunRecord's run-id shape ("<task>/<box>").
func auditRunID(task supervisor.Task, handle supervisor.BoxHandle) string {
	parts := make([]string, 0, 2)
	if id := strings.TrimSpace(task.ID); id != "" {
		parts = append(parts, id)
	}
	if id := strings.TrimSpace(handle.ID); id != "" {
		parts = append(parts, id)
	}
	if len(parts) == 0 {
		return "run"
	}
	return strings.Join(parts, "/")
}

// auditVerdict maps a gate verdict onto the typed audit verdict.
func auditVerdict(verdict gate.Verdict) audit.AuditVerdict {
	if verdict.OK {
		return audit.VerdictPass
	}
	return audit.VerdictFail
}

func writeStdout(streams supervisor.RunStreams, format string, args ...any) {
	if streams.Stdout == nil {
		return
	}
	_, _ = fmt.Fprintf(streams.Stdout, format, args...)
}

func writeStderr(streams supervisor.RunStreams, format string, args ...any) {
	if streams.Stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(streams.Stderr, format, args...)
}

func newProductionGate() (supervisor.Gate, error) {
	verifier, err := gate.New(
		gate.GoBuildStep{},
		gate.GoVetStep{},
		gate.GoTestStep{},
		gate.GoFmtStep{},
		gate.GolangciLintStep{},
		gate.DepScanStep{},
		gate.CodeScannerStep{},
	)
	if err != nil {
		return nil, fmt.Errorf("construct production gate: %w", err)
	}
	return verifier, nil
}
