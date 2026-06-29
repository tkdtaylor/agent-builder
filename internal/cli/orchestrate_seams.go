package cli

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/worker"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// generateSealKeyPair is the seam used to generate X25519 seal keypairs for the
// in-process v1 wire. Tests may override this variable to inject a failure without
// non-portable crypto/rand fault injection (SEC-001 fault-injection seam).
var generateSealKeyPair = envelope.GenerateKeyPair

// newTransportDispatch returns the live DispatchFunc for the orchestrate path. It
// round-trips every sub-goal through the worker envelope transport (ADR 048) before
// declaring the dispatch done:
//
//  1. Seal+sign the work-item and replay-check it against the ONE shared
//     work-item-direction ReplayCache (083 SEC-001) — a replayed work-item nonce is
//     rejected with envelope.ErrReplay and the worker never runs.
//  2. Run the worker through the existing per-worker runtime assembly (runtime.Run).
//  3. Seal+sign the worker's result and replay-check it against the ONE shared
//     result-direction ReplayCache (083 SEC-001) — closing the replay window on the
//     return path too. Both caches are long-lived (one per direction), never
//     reconstructed per dispatch.
//
// The X25519 seal keys for the in-process v1 wire (ADR 048) are generated once at
// assembly time — the seal layer provides confidentiality + tamper-evidence on the
// in-process hop; the long-lived, file-backed, fail-closed-checked key is the
// Ed25519 signing key (signingKey), which is what 083 SEC-003 guards.
//
// Returns a non-nil error if keypair generation fails (SEC-001: fail fast on
// crypto/rand failure rather than silently sealing under zero keys).
func newTransportDispatch(signingKey ed25519.PrivateKey, workItemCache, resultCache *envelope.ReplayCache, sink audit.Sink, logger *slog.Logger) (orchestrator.DispatchFunc, error) {
	// Generate the X25519 seal keypairs for the in-process wire once. Both ends are
	// in-process, so the orchestrator owns both halves of the seal for v1.
	orchXPub, orchXPriv, err := generateSealKeyPair()
	if err != nil {
		return nil, fmt.Errorf("newTransportDispatch: generate seal keypair (orch): %w", err)
	}
	workerXPub, workerXPriv, err := generateSealKeyPair()
	if err != nil {
		return nil, fmt.Errorf("newTransportDispatch: generate seal keypair (worker): %w", err)
	}

	signPub := signingKey.Public().(ed25519.PublicKey)

	// Work-item direction: orchestrator -> worker.
	workItemSender := worker.NewWorkItemSender(worker.SenderConfig{
		EdPriv: signingKey, XPriv: orchXPriv, RecipPub: workerXPub, Logger: logger,
	})
	workItemReceiver := worker.NewWorkItemReceiver(worker.ReceiverConfig{
		SignPub: signPub, RecipPriv: workerXPriv, SenderPub: orchXPub,
		ReplayCache: workItemCache, // the ONE shared work-item cache (083 SEC-001)
		AuditSink:   sink, Logger: logger,
	})
	// Result direction: worker -> orchestrator. The worker signs results with the
	// same key material in the in-process v1 wire; a future out-of-process worker
	// supplies its own keypair without changing this seam.
	resultSender := worker.NewResultSender(worker.SenderConfig{
		EdPriv: signingKey, XPriv: workerXPriv, RecipPub: orchXPub, Logger: logger,
	})
	resultReceiver := worker.NewResultReceiver(worker.ReceiverConfig{
		SignPub: signPub, RecipPriv: orchXPriv, SenderPub: workerXPub,
		ReplayCache: resultCache, // the ONE shared result cache (083 SEC-001)
		AuditSink:   sink, Logger: logger,
	})

	dispatch := orchestrator.DispatchFunc(func(ctx context.Context, sub orchestrator.SubGoal, base runtimewiring.Config) error {
		env, err := workItemSender.DispatchWorkItem(sub.Task)
		if err != nil {
			return fmt.Errorf("orchestrate dispatch: seal work-item for %q: %w", sub.Task.ID, err)
		}
		// Replay-check + verify against the SHARED work-item cache.
		if _, err := workItemReceiver.ReceiveWorkItem(env); err != nil {
			return fmt.Errorf("orchestrate dispatch: verify work-item for %q: %w", sub.Task.ID, err)
		}
		// Transport accepted the work-item — run the worker through the existing
		// per-worker runtime assembly (the orchestrator never reimplements it).
		cfg := base
		cfg.RecipeName = sub.RecipeName
		if runErr := runtimewiring.Run(cfg, io.Discard); runErr != nil {
			return runErr
		}
		// Replay-check the worker's result envelope against the SHARED result cache,
		// closing the replay window on the return path.
		resEnv, err := resultSender.DispatchResult(supervisor.Result{OK: true})
		if err != nil {
			return fmt.Errorf("orchestrate dispatch: seal result for %q: %w", sub.Task.ID, err)
		}
		if _, err := resultReceiver.ReceiveResult(resEnv); err != nil {
			return fmt.Errorf("orchestrate dispatch: verify result for %q: %w", sub.Task.ID, err)
		}
		return nil
	})
	return dispatch, nil
}

// logReporter is the fallback outbound Reporter: it writes the rendered text to the
// given writer (task 098 wires a real outbound channel; this is the no-channel
// fallback so the orchestrator's approval/result reports are never silently dropped).
//
// Concurrency (ADR 054 §1, task 112): the async control plane shares ONE Reporter
// across all concurrent goal actors — each actor's dispatchPlan calls Report. A
// plain io.Writer is not safe for concurrent writes, so Report serializes on a
// mutex. The writer is shared, but each Report is one atomic line.
type logReporter struct {
	mu *sync.Mutex
	w  io.Writer
}

func newLogReporter(w io.Writer) supervisor.Reporter {
	if w == nil {
		w = io.Discard
	}
	return logReporter{mu: &sync.Mutex{}, w: w}
}

func (r logReporter) Report(_ context.Context, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := fmt.Fprintln(r.w, text)
	return err
}

// EnvGoalSpec / EnvGoalID / EnvGoalRepo configure the single-goal env input the
// validation harness uses to feed a goal to the orchestrate subcommand without an
// inbound channel. Under the generalized inbound seam (ADR 054 §2) these are read by
// envMessageSource (router.go), which delivers AGENT_BUILDER_GOAL_SPEC as the first
// MsgNewGoal before any stdin line.
const (
	EnvGoalSpec = "AGENT_BUILDER_GOAL_SPEC"
	EnvGoalID   = "AGENT_BUILDER_GOAL_ID"
	EnvGoalRepo = "AGENT_BUILDER_GOAL_REPO"
)

// filepathJoinTemp joins name onto the OS temp dir (small helper to keep the
// orchestrate.go import set minimal).
func filepathJoinTemp(name string) string {
	return filepath.Join(os.TempDir(), name)
}
