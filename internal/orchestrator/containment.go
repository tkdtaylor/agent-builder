package orchestrator

// Containment, egress posture, and the self-repo bright line (task 085 / ADR 050).
//
// The orchestrator is itself privileged, network-connected, and long-lived (ADR
// 042), so it must run under the SAME containment profile as the workers it
// dispatches: rootless, read-only rootfs, resource-limited, default-deny egress.
// This file declares the L2-assertable posture the orchestrator's run record
// carries. The live enforcement (rootless Podman + runsc, nftables egress) is L6
// operator-run on a provisioned host (ADR 050 §3) — not claimed in CI.

// ContainmentProfileExecSandbox is the containment profile the orchestrator runs
// under — identical to the worker exec-sandbox profile (ADR 035/050). The run
// record carries this string so an operator (and TC-085-01) can confirm the
// orchestrator is contained like a worker, not running unconfined.
const ContainmentProfileExecSandbox = "exec-sandbox"

// EgressDefaultDeny is the orchestrator's egress posture: outbound network is
// default-deny (the same nftables allowlist model the worker boxes use). Asserted
// at L2 (TC-085-04); the live nftables block of a non-allowlisted host is L6
// operator-deferred (ADR 050 §3).
const EgressDefaultDeny = "default-deny"

// OwnRepo is the agent-builder self-repo. The orchestrator refuses to dispatch any
// worker whose target_repo or result sink is this repo (ADR 042's non-negotiable
// bright line: no agent at any tier edits agent-builder's own repo). The deny is
// fail-closed and independent of the policy file (REQ-085-05a). A static fitness
// check (F-013, make fitness-no-self-repo-sink) is the belt to this suspenders
// (REQ-085-05b).
const OwnRepo = "github.com/tkdtaylor/agent-builder"

// Containment is the L2-assertable containment posture the orchestrator's run
// record carries (task 085 / ADR 050 §3). It mirrors the worker box posture so
// the orchestrator is contained, not merely its workers. The live enforcement of
// each field is L6 operator-run.
type Containment struct {
	// Profile is the containment profile name (ContainmentProfileExecSandbox).
	Profile string
	// Rootless reports that the box runs rootless (no root in the container).
	Rootless bool
	// ReadOnlyRootfs reports that the container rootfs is mounted read-only.
	ReadOnlyRootfs bool
	// ResourceLimited reports that CPU/memory/pids resource limits are applied.
	ResourceLimited bool
	// EgressPolicy is the outbound network posture (EgressDefaultDeny).
	EgressPolicy string
}

// defaultContainment is the orchestrator's standard posture: the exec-sandbox
// profile with rootless + read-only rootfs + resource limits + default-deny
// egress — the same profile a worker box runs under (ADR 050 §3).
func defaultContainment() Containment {
	return Containment{
		Profile:         ContainmentProfileExecSandbox,
		Rootless:        true,
		ReadOnlyRootfs:  true,
		ResourceLimited: true,
		EgressPolicy:    EgressDefaultDeny,
	}
}

// Containment returns the orchestrator's containment posture (REQ-085-01/04). The
// posture is the L2 evidence that the orchestrator is contained like a worker;
// the live Podman/runsc/nftables enforcement is L6 operator-run.
func (o *Orchestrator) Containment() Containment {
	return o.containment
}

// SelfRepoSinkViolation reports whether a block of recipe source text declares the
// agent-builder own-repo as a result-sink / publish target. It is the detection
// predicate behind the static fitness check F-013 (make fitness-no-self-repo-sink,
// REQ-085-05b): a true return means a registered recipe would target the own-repo
// as a sink, which the bright line forbids by construction.
//
// The check is a conservative substring match for the canonical own-repo path
// (OwnRepo) appearing in source that also references a sink/remote/publish target.
// It deliberately errs toward flagging: a recipe source that names the own-repo
// near a sink/remote token is a violation. Comments and import lines are excluded
// by the caller (the fitness script scopes to recipe source, not arbitrary files).
func SelfRepoSinkViolation(source string) bool {
	if !containsOwnRepo(source) {
		return false
	}
	// The own-repo path appears; treat it as a sink violation only when it co-occurs
	// with a sink/remote/publish indicator on the same logical declaration.
	for _, token := range []string{"Sink", "sink", "Remote", "remote", "Publish", "publish", "PublishRemote"} {
		if containsToken(source, token) {
			return true
		}
	}
	return false
}

func containsOwnRepo(s string) bool {
	return containsToken(s, OwnRepo)
}

// containsToken is a tiny stdlib-free substring check so containment.go imports
// nothing beyond the package itself (keeping the predicate trivially portable).
func containsToken(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// targetsOwnRepo reports whether a sub-goal's target repo or result sink is the
// agent-builder self-repo. It is the runtime predicate behind REQ-085-05a: a true
// return means the orchestrator must refuse to dispatch the worker, fail-closed,
// regardless of the policy decision. Matching is exact on OwnRepo (the policy
// resource carries the canonical repo path).
func targetsOwnRepo(sub SubGoal) bool {
	return sub.TargetRepo == OwnRepo || sub.Sink == OwnRepo
}
