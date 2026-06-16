// Package runtime assembles the concrete Phase 0 run pipeline for the CLI.
package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

	if err := supervisor.New(options...).Run(); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "run completed: task %s\n", task.ID)
	return nil
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
	_, exitCode, err := b.runner.Run(sandbox.Request{
		Command:  []string{"/bin/true"},
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

func (l retryingInBoxLoop) RunInside(_ supervisor.BoxHandle, task supervisor.Task, streams supervisor.RunStreams) error {
	writeCommand(streams, "containment=podman launcher=%s", l.launcher)
	writeCommand(streams, "pick task %s", task.ID)
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
		case agentloop.StateVerify:
			writeCommand(streams, "verify worktree %s", l.worktree)
		}
	}

	switch outcome.Kind {
	case agentloop.RetryOutcomeDone:
		writeStdout(streams, "executor attempt completed: branch=%s\n", outcome.Branch)
		writeStdout(streams, "gate passed: %s\n", summarizeVerdict(outcome.LastOutcome.Verdict))
		writeCommand(streams, "publish branch %s remote=%s", outcome.Branch, l.redact(l.publishRemote))
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
		return nil
	case agentloop.RetryOutcomeEscalated:
		writeFailureEvidence(streams, outcome.LastOutcome)
		writeStderr(streams, "task %s escalated after %d attempts\n", task.ID, outcome.Attempts)
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
