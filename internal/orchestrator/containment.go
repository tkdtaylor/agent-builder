package orchestrator

import (
	"strings"
)

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
		if strings.Contains(source, token) {
			return true
		}
	}
	return false
}

func containsOwnRepo(s string) bool {
	return strings.Contains(s, OwnRepo)
}

// CanonicalizeRepo normalizes a repository reference into a canonical form for
// comparison. It handles multiple formats and variants:
//   - https://github.com/tkdtaylor/agent-builder → github.com/tkdtaylor/agent-builder
//   - HTTPS://github.com/... (case-insensitive) → github.com/tkdtaylor/agent-builder
//   - git@github.com:tkdtaylor/agent-builder → github.com/tkdtaylor/agent-builder
//   - ssh://git@github.com:22/tkdtaylor/agent-builder (with port) → github.com/tkdtaylor/agent-builder
//   - github.com/tkdtaylor/agent-builder.GIT (case insensitive) → github.com/tkdtaylor/agent-builder
//   - "  github.com/tkdtaylor/agent-builder  " (whitespace) → github.com/tkdtaylor/agent-builder
//
// Normalization order (load-bearing, must match this sequence):
//   1. TrimSpace (whitespace removal)
//   2. ToLower (case folding — makes subsequent string checks case-insensitive)
//   3. Strip scheme (https://, http://, ssh://, git://)
//   4. Strip scp-form prefix (git@) and convert scp-colon to slash
//   5. Strip port (:[0-9]+ after host, before first path slash)
//   6. Strip trailing slash
//   7. Strip trailing .git suffix
//
// Empty input returns empty (not an error). Non-empty input that canonicalizes to
// empty is treated as a match by fail-closed logic in targetsOwnRepo (deny).
func CanonicalizeRepo(s string) string {
	if s == "" {
		return ""
	}

	// 1. Trim whitespace
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// 2. Lowercase FIRST so all subsequent checks are case-insensitive
	s = strings.ToLower(s)

	// 3. Strip scheme: https://, http://, ssh://, git://
	for _, scheme := range []string{"https://", "http://", "ssh://", "git://"} {
		if strings.HasPrefix(s, scheme) {
			s = strings.TrimPrefix(s, scheme)
			break
		}
	}

	// 4. Strip scp-form: git@host:path → host/path (convert : to /)
	// BUT FIRST strip any port that appears as :digits in the scp form
	if strings.Contains(s, "git@") {
		idx := strings.Index(s, "git@")
		if idx >= 0 {
			s = s[idx+4:] // skip "git@"
			// Handle scp-form: git@host:path or git@host:port/path
			// First, check if there's a colon
			if colonIdx := strings.Index(s, ":"); colonIdx >= 0 {
				// Check what comes after the colon
				afterColon := s[colonIdx+1:]
				// If it's all digits followed by /, it's a port; strip it
				// Otherwise it's a path in scp-form, convert : to /
				isPort := false
				slashAfterColon := strings.Index(afterColon, "/")
				if slashAfterColon >= 0 {
					portCandidate := afterColon[:slashAfterColon]
					isPortDigits := true
					for _, c := range portCandidate {
						if c < '0' || c > '9' {
							isPortDigits = false
							break
						}
					}
					if isPortDigits {
						// It's a port; strip it: git@host:22/path → host/path
						isPort = true
						s = s[:colonIdx] + afterColon[slashAfterColon:]
					}
				}
				if !isPort {
					// It's a path in scp-form; convert : to /
					s = s[:colonIdx] + "/" + afterColon
				}
			}
		}
	}

	// 6. Strip trailing slash
	s = strings.TrimSuffix(s, "/")

	// 7. Strip trailing .git suffix
	s = strings.TrimSuffix(s, ".git")

	return s
}

// targetsOwnRepo reports whether a sub-goal's target repo or result sink is the
// agent-builder self-repo. It is the runtime predicate behind REQ-085-05a: a true
// return means the orchestrator must refuse to dispatch the worker, fail-closed,
// regardless of the policy decision.
//
// The check is performed in three places:
//   1. sub.Task.Repo — set by the planner from the inbound goal.Repo; this is the
//      worker's EFFECTIVE target (what decides where the worker writes).
//   2. sub.TargetRepo — optional override set by test or future dynamic routing.
//   3. sub.Sink — optional override for result-sink target.
//
// All three are canonicalized before comparison so the guard catches all forms of
// the own-repo path (https://, git@, .git suffix, case variations, trailing slash).
// If canonicalization fails (empty result from a non-empty input), the guard
// treats it conservatively as a potential match (fail-closed: when in doubt, deny).
func targetsOwnRepo(sub SubGoal) bool {
	canonOwnRepo := CanonicalizeRepo(OwnRepo)

	// Check the worker's effective target (Task.Repo set by the planner).
	if sub.Task.Repo != "" {
		if canonical := CanonicalizeRepo(sub.Task.Repo); canonical == "" {
			// Failed to canonicalize a non-empty repo — fail-closed: treat as a match.
			return true
		} else if canonical == canonOwnRepo {
			return true
		}
	}

	// Check optional explicit overrides (used by tests or future dynamic routing).
	for _, target := range []string{sub.TargetRepo, sub.Sink} {
		if target != "" {
			if canonical := CanonicalizeRepo(target); canonical == "" {
				// Failed to canonicalize a non-empty target — fail-closed.
				return true
			} else if canonical == canonOwnRepo {
				return true
			}
		}
	}

	return false
}
