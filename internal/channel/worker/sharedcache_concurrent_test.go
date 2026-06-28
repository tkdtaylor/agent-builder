package worker_test

// Task 086 carry-forward from task 083 SEC-001 (was MEDIUM, now on the LIVE concurrent
// dispatch path): when the orchestrator dispatches N workers concurrently, replay
// protection requires ONE long-lived shared *envelope.ReplayCache per direction across
// ALL workers — never a fresh cache per worker (a virgin cache accepts a replayed
// envelope). This test exercises that invariant directly:
//
//   1. The shared cache is safe under concurrent Receiver use (run under -race).
//   2. A nonce accepted by one receiver is rejected as a replay by a concurrent
//      receiver sharing the SAME cache — i.e. the shared cache, not per-worker caches,
//      is what closes the replay hole on the concurrent wire.

import (
	"errors"
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TestSharedReplayCacheConcurrentSafe — N receivers built over ONE shared cache each
// receive a DISTINCT work-item concurrently. With a goroutine-safe shared cache and
// distinct nonces, all N must succeed and the race detector must stay clean. This is
// the live-path shape: one shared cache, many concurrent workers.
func TestSharedReplayCacheConcurrentSafe(t *testing.T) {
	k := newKeyset(t)
	sender := k.workItemSender()
	const n = 16

	// One shared cache across all concurrent receivers (the 083 SEC-001 invariant).
	cache := envelope.NewReplayCache(0)
	sink := audit.NewFakeSink()

	// Pre-build N distinct envelopes (distinct nonces via fresh crypto/rand per seal).
	envs := make([]envelope.Envelope, n)
	for i := range envs {
		env, err := sender.DispatchWorkItem(supervisor.Task{ID: "sg", Spec: "concurrent work"})
		if err != nil {
			t.Fatalf("DispatchWorkItem[%d]: %v", i, err)
		}
		envs[i] = env
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var accepted int
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			// Each worker builds its own Receiver but over the SHARED cache.
			recv := k.workItemReceiver(sink, cache)
			if _, err := recv.ReceiveWorkItem(envs[i]); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if accepted != n {
		t.Fatalf("distinct-nonce concurrent receive: accepted %d, want %d", accepted, n)
	}
}

// TestSharedReplayCacheRejectsReplayAcrossWorkers — the SAME envelope delivered to two
// DIFFERENT receivers sharing one cache is accepted exactly once and rejected as a
// replay the second time. A per-worker (virgin) cache would accept it twice — proving
// the shared cache is the load-bearing control on the concurrent path.
func TestSharedReplayCacheRejectsReplayAcrossWorkers(t *testing.T) {
	k := newKeyset(t)
	sender := k.workItemSender()
	cache := envelope.NewReplayCache(0)
	sink := audit.NewFakeSink()

	env, err := sender.DispatchWorkItem(supervisor.Task{ID: "sg-1", Spec: "once"})
	if err != nil {
		t.Fatalf("DispatchWorkItem: %v", err)
	}

	// Receiver A (worker A) accepts.
	recvA := k.workItemReceiver(sink, cache)
	if _, err := recvA.ReceiveWorkItem(env); err != nil {
		t.Fatalf("receiver A first delivery: %v", err)
	}

	// Receiver B (a DIFFERENT worker) over the SAME cache: replay → rejected.
	recvB := k.workItemReceiver(sink, cache)
	_, err = recvB.ReceiveWorkItem(env)
	if err == nil {
		t.Fatal("receiver B (shared cache) accepted a replayed envelope — replay hole; a fresh per-worker cache would do this")
	}
	if !errors.Is(err, envelope.ErrReplay) {
		t.Fatalf("receiver B replay error = %v, want errors.Is ErrReplay", err)
	}
}
