.PHONY: lint format test fitness fitness-no-docker fitness-gate-blocking fitness-supervisor-isolation fitness-orchestrator-no-executor fitness-no-srt fitness-audit-isolation fitness-exec-sandbox-default fitness-policy-isolation fitness-envelope-isolation fitness-worker-transport-isolation fitness-diagrams-render fitness-memoryguard-isolation fitness-no-self-repo-sink check l6-preflight l6-probe

lint:
	golangci-lint run

format:
	gofmt -w .

test:
	go test ./...

# Fitness functions — see docs/spec/fitness-functions.md
fitness: fitness-no-docker fitness-gate-blocking fitness-supervisor-isolation fitness-orchestrator-no-executor fitness-no-srt fitness-audit-isolation fitness-exec-sandbox-default fitness-policy-isolation fitness-envelope-isolation fitness-worker-transport-isolation fitness-diagrams-render fitness-memoryguard-isolation fitness-no-self-repo-sink
	@echo "All fitness checks passed."

fitness-no-docker:
	@word=$$(printf 'dock%s' 'er'); \
	cap_word=$$(printf 'Dock%s' 'er'); \
	compose="$${word}-compose"; \
	imagefile="$${cap_word}file"; \
	content_pattern="(^|[^[:alnum:]_-])($${word}|$${compose}|$${imagefile})([^[:alnum:]_-]|$$)"; \
	matches=$$( \
		find . \
			\( -path './.git' -o -path './.claude' -o -path './.codex' -o -path './.agents' -o -path './vendor' -o -path './node_modules' -o -path './containment' \) -prune -o \
			-type f \
			! -path './docs/tasks/*' \
			! -path './docs/plans/*' \
			! -path './docs/architecture/*.md' \
			! -path './docs/architecture/decisions/*' \
			! -path './docs/spec/SPEC.md' \
			! -path './docs/spec/fitness-functions.md' \
			! -path './AGENTS.md' \
			! -path './CLAUDE.md' \
			! -path './docs/agent-rules.md' \
			-print | while IFS= read -r file; do \
				base=$${file##*/}; \
				base_lc=$$(printf '%s' "$$base" | tr '[:upper:]' '[:lower:]'); \
				imagefile_lc="$${word}file"; \
				case "$$base_lc" in \
					"$$imagefile_lc"|"$$imagefile_lc".*|"$$compose".yml|"$$compose".yaml) \
						printf '%s\n' "$$file"; \
						continue; \
						;; \
				esac; \
				if grep -IiqE "$$content_pattern" "$$file"; then \
					printf '%s\n' "$$file"; \
				fi; \
			done \
	); \
	if [ -n "$$matches" ]; then \
		echo "FAIL fitness-no-docker: forbidden dev-environment reference(s) found:"; \
		printf '%s\n' "$$matches" | sort -u; \
		exit 1; \
	fi; \
	echo "PASS fitness-no-docker: no forbidden dev-environment references found."

fitness-gate-blocking:
	@matches=$$( \
		find cmd/agent-builder internal/gate \
			-type f \
			-name '*.go' \
			! -name '*_test.go' \
			-print | while IFS= read -r file; do \
				awk ' \
					function strip_comments(line, out, block_start, line_comment, block_end) { \
						out = ""; \
						while (length(line) > 0) { \
							if (in_block) { \
								block_end = index(line, "*/"); \
								if (block_end == 0) { return out; } \
								line = substr(line, block_end + 2); \
								in_block = 0; \
							} else { \
								block_start = index(line, "/*"); \
								line_comment = index(line, "//"); \
								if (line_comment > 0 && (block_start == 0 || line_comment < block_start)) { \
									return out substr(line, 1, line_comment - 1); \
								} \
								if (block_start > 0) { \
									out = out substr(line, 1, block_start - 1); \
									line = substr(line, block_start + 2); \
									in_block = 1; \
								} else { \
									return out line; \
								} \
							} \
						} \
						return out; \
					} \
					{ \
						code = strip_comments($$0); \
						if (code ~ /--no-verify|--skip-verify|no-verify|skip-verify/) { \
							printf "%s:%d: forbidden verify skip flag: %s\n", FILENAME, FNR, code; \
						} \
						if (code ~ /(^|[^[:alnum:]_])(NO_VERIFY|SKIP_SCAN|SKIP_DEP_SCAN|SKIP_CODE_SCANNER|SKIP_VERIFY|BYPASS_VERIFY)([^[:alnum:]_]|$$)/) { \
							printf "%s:%d: forbidden scanner skip env var: %s\n", FILENAME, FNR, code; \
						} \
						if (code ~ /(^|[^[:alnum:]_])(if|return)([^[:alnum:]_].*)?(skip|bypass|noVerify|skipScan|skipDepScan|skipCodeScanner|skipVerify|bypassVerify)([^[:alnum:]_]|$$)/ || code ~ /(^|[^[:alnum:]_])(skip|bypass|noVerify|skipScan|skipDepScan|skipCodeScanner|skipVerify|bypassVerify)([^[:alnum:]_].*)?(if|return)([^[:alnum:]_]|$$)/) { \
							printf "%s:%d: forbidden conditional scanner bypass: %s\n", FILENAME, FNR, code; \
						} \
					} \
				' "$$file"; \
			done \
	); \
	if [ -n "$$matches" ]; then \
		echo "FAIL fitness-gate-blocking: verification gate bypass affordance(s) found:"; \
		printf '%s\n' "$$matches" | sort -u; \
		exit 1; \
	fi; \
	echo "PASS fitness-gate-blocking: no verification gate bypass affordances found."

fitness-supervisor-isolation:
	@deps=$$(go list -deps ./internal/supervisor/...) || exit $$?; \
	forbidden=$$(printf '%s\n' "$$deps" | awk '/(^|\/)(executor|executors|llm|llms|web|webfetch|web-fetch)(\/|$$)/ { print }'); \
	if [ -n "$$forbidden" ]; then \
		echo "FAIL fitness-supervisor-isolation: supervisor imports forbidden package(s):"; \
		printf '%s\n' "$$forbidden"; \
		exit 1; \
	fi; \
	echo "PASS fitness-supervisor-isolation: supervisor import graph contains no executor/LLM/web-fetch packages."

# fitness-orchestrator-no-executor covers test-spec TC-081-05 (ADR 046 D-2):
# internal/orchestrator must have NO DIRECT import of internal/executor. The
# orchestrator authors no code; it dispatches workers THROUGH internal/runtime,
# which legitimately imports internal/executor — that transitive path is the
# ADR-042-blessed dispatch path and is expected. So this check asserts DIRECT
# imports only (go list -f '{{.Imports}}'), NOT the transitive graph (-deps).
fitness-orchestrator-no-executor:
	@imports=$$(go list -f '{{ join .Imports "\n" }}' ./internal/orchestrator) || exit $$?; \
	forbidden=$$(printf '%s\n' "$$imports" | grep '/internal/executor\(/\|$$\)' || true); \
	if [ -n "$$forbidden" ]; then \
		echo "FAIL fitness-orchestrator-no-executor: internal/orchestrator DIRECTLY imports internal/executor:"; \
		printf '%s\n' "$$forbidden"; \
		exit 1; \
	fi; \
	echo "PASS fitness-orchestrator-no-executor: internal/orchestrator has no direct import of internal/executor."

# fitness-no-srt covers test-spec TC-036-04: the default run pipeline must not
# transitively import internal/sandbox/sandboxruntime.
fitness-no-srt:
	@deps=$$(go list -deps ./internal/runtime/...) || exit $$?; \
	forbidden=$$(printf '%s\n' "$$deps" | grep 'sandboxruntime' || true); \
	if [ -n "$$forbidden" ]; then \
		echo "FAIL fitness-no-srt: internal/runtime transitively imports sandboxruntime:"; \
		printf '%s\n' "$$forbidden"; \
		exit 1; \
	fi; \
	echo "PASS fitness-no-srt: internal/runtime does not import sandboxruntime"

# fitness-audit-isolation covers test-spec TC-042-01, TC-042-02, TC-042-03, TC-042-04:
# internal/audit must be a leaf package — no executor/LLM/web-fetch imports and no
# audit-trail Go module import (the block is reached over os/exec, not a Go import —
# ADR 026 Option A). Also asserts that wiring internal/audit into the supervisor
# (task 041) did not drag any executor/LLM/web package into the supervisor's
# transitive import graph.
fitness-audit-isolation:
	@audit_deps=$$(go list -deps ./internal/audit/...) || exit $$?; \
	audit_forbidden=$$(printf '%s\n' "$$audit_deps" | awk '/(^|\/)(executor|executors|llm|llms|web|webfetch|web-fetch)(\/|$$)/ { print }'); \
	audit_trail_import=$$(printf '%s\n' "$$audit_deps" | grep 'audit-trail' || true); \
	sup_deps=$$(go list -deps ./internal/supervisor/...) || exit $$?; \
	sup_forbidden=$$(printf '%s\n' "$$sup_deps" | awk '/(^|\/)(executor|executors|llm|llms|web|webfetch|web-fetch)(\/|$$)/ { print }'); \
	if [ -n "$$audit_forbidden" ]; then \
		echo "FAIL fitness-audit-isolation: internal/audit imports forbidden executor/LLM/web package(s):"; \
		printf '%s\n' "$$audit_forbidden"; \
		exit 1; \
	fi; \
	if [ -n "$$audit_trail_import" ]; then \
		echo "FAIL fitness-audit-isolation: internal/audit imports audit-trail as a Go module (must use os/exec — ADR 026):"; \
		printf '%s\n' "$$audit_trail_import"; \
		exit 1; \
	fi; \
	if [ -n "$$sup_forbidden" ]; then \
		echo "FAIL fitness-audit-isolation: supervisor's audit dependency drags in forbidden executor/LLM/web package(s):"; \
		printf '%s\n' "$$sup_forbidden"; \
		exit 1; \
	fi; \
	echo "PASS fitness-audit-isolation: internal/audit import graph contains no executor/LLM/web-fetch or audit-trail-block packages and the supervisor's audit dependency drags none in."

# fitness-exec-sandbox-default verifies that internal/runtime wires execsandbox as the
# default run backend (TC-062-06, TC-062-07). The runtime package must import
# internal/sandbox/execsandbox in its default path.
fitness-exec-sandbox-default:
	@runtime_deps=$$(go list -deps ./internal/runtime/...) || exit $$?; \
	exec_sandbox=$$(printf '%s\n' "$$runtime_deps" | grep 'internal/sandbox/execsandbox' || true); \
	if [ -z "$$exec_sandbox" ]; then \
		echo "FAIL fitness-exec-sandbox-default: internal/runtime does not import internal/sandbox/execsandbox:"; \
		echo "internal/runtime must wire execsandbox as the default backend (ADR 035, TC-062-06)"; \
		exit 1; \
	fi; \
	echo "PASS fitness-exec-sandbox-default: internal/runtime wires execsandbox as the default run backend"

# fitness-policy-isolation covers test-spec TC-074-01, TC-074-02, TC-074-03, TC-074-04:
# (1) internal/policy must be a leaf — its import graph must not contain any other
#     agent-builder/internal/ path. This prevents internal/policy from importing
#     internal/runtime, internal/sandbox, internal/vault, etc., which would allow an
#     in-process decision path that bypasses the out-of-process rule.
# (2) internal/runtime must reach the policy-engine block only over IPC — its import
#     graph must NOT contain github.com/tkdtaylor/policy-engine (the block's Go module
#     path). If that import appears, the block's in-process Decide() is reachable and
#     the out-of-process security model is defeated (ADR 038).
fitness-policy-isolation:
	@policy_deps=$$(go list -deps ./internal/policy/...) || exit $$?; \
	policy_forbidden=$$(printf '%s\n' "$$policy_deps" | grep 'github.com/tkdtaylor/agent-builder/internal/' | grep -v 'agent-builder/internal/policy' || true); \
	runtime_deps=$$(go list -deps ./internal/runtime/...) || exit $$?; \
	runtime_block_import=$$(printf '%s\n' "$$runtime_deps" | grep 'github.com/tkdtaylor/policy-engine' || true); \
	if [ -n "$$policy_forbidden" ]; then \
		echo "FAIL fitness-policy-isolation: internal/policy imports forbidden agent-builder/internal package(s):"; \
		printf '%s\n' "$$policy_forbidden"; \
		exit 1; \
	fi; \
	if [ -n "$$runtime_block_import" ]; then \
		echo "FAIL fitness-policy-isolation: internal/runtime imports policy-engine as a Go module (must use IPC — ADR 038):"; \
		printf '%s\n' "$$runtime_block_import"; \
		exit 1; \
	fi; \
	echo "PASS fitness-policy-isolation: internal/policy import graph contains no other internal packages, and internal/runtime does not import the policy-engine block as a Go module."

# fitness-envelope-isolation covers test-spec TC-096-10, TC-096-11:
# internal/envelope must be a pure leaf — its import graph must contain only stdlib +
# golang.org/x/crypto, with NO agent-builder/internal/ paths other than itself.
# Further, internal/envelope must NOT appear in internal/supervisor's dependency graph
# (F-007). The envelope package holds crypto primitives and must stay strictly outside
# the supervisor's control path (the GoalSource seam separates the channel/crypto side
# from the orchestrator side; ADR 045 §1).
fitness-envelope-isolation:
	@envelope_deps=$$(go list -deps ./internal/envelope/...) || exit $$?; \
	envelope_forbidden=$$(printf '%s\n' "$$envelope_deps" | grep 'github.com/tkdtaylor/agent-builder/internal/' | grep -v 'agent-builder/internal/envelope' || true); \
	supervisor_deps=$$(go list -deps ./internal/supervisor/...) || exit $$?; \
	supervisor_envelope=$$(printf '%s\n' "$$supervisor_deps" | grep 'github.com/tkdtaylor/agent-builder/internal/envelope' || true); \
	if [ -n "$$envelope_forbidden" ]; then \
		echo "FAIL fitness-envelope-isolation: internal/envelope imports forbidden agent-builder/internal package(s):"; \
		printf '%s\n' "$$envelope_forbidden"; \
		exit 1; \
	fi; \
	if [ -n "$$supervisor_envelope" ]; then \
		echo "FAIL fitness-envelope-isolation: internal/envelope appears in internal/supervisor's dependency graph:"; \
		printf '%s\n' "$$supervisor_envelope"; \
		exit 1; \
	fi; \
	echo "PASS fitness-envelope-isolation: internal/envelope is not in internal/supervisor's dependency graph."

# fitness-worker-transport-isolation covers test-spec TC-083-04 (F-011, ADR 048 §3):
# internal/channel/worker (the orchestrator↔worker transport adapter) must be a leaf:
# its DIRECT agent-builder/internal/ imports must be exactly internal/envelope,
# internal/supervisor, and internal/audit — nothing else. envelope is the
# sign/verify/seal/replay primitive; supervisor supplies the Task/Result seam types;
# audit supplies the rejection-event Sink seam (itself a verified leaf via F-005).
#
# This is a DIRECT-import assertion (go list .Imports), not a -deps transitive one, for
# the same reason as F-010: internal/supervisor legitimately drags in internal/gate and
# internal/sandbox transitively (it is the trusted control core), so a -deps check would
# false-positive on that blessed path. The security intent — keep internal/executor,
# internal/runtime, internal/orchestrator, LLM, and web-fetch code off the transport — is
# fully covered by a direct-import check: none of the three allowed leaves imports the
# executor/runtime/orchestrator (guaranteed by F-003/F-005/F-007), so the transport can
# only reach them by importing them directly, which this check blocks.
fitness-worker-transport-isolation:
	@imports=$$(go list -f '{{ join .Imports "\n" }}' ./internal/channel/worker) || exit $$?; \
	worker_forbidden=$$(printf '%s\n' "$$imports" \
		| grep 'github.com/tkdtaylor/agent-builder/internal/' \
		| grep -v 'agent-builder/internal/envelope\(/\|$$\)' \
		| grep -v 'agent-builder/internal/supervisor\(/\|$$\)' \
		| grep -v 'agent-builder/internal/audit\(/\|$$\)' || true); \
	if [ -n "$$worker_forbidden" ]; then \
		echo "FAIL fitness-worker-transport-isolation: internal/channel/worker DIRECTLY imports forbidden agent-builder/internal package(s):"; \
		printf '%s\n' "$$worker_forbidden"; \
		exit 1; \
	fi; \
	echo "PASS fitness-worker-transport-isolation: internal/channel/worker directly imports only envelope, supervisor, and audit internal packages."

# fitness-memoryguard-isolation covers test-spec TC-084-03 (F-012, ADR 049):
# internal/memoryguard must be a leaf — its transitive dependency graph must
# contain no github.com/tkdtaylor/agent-builder/internal/ path other than
# internal/memoryguard itself (only stdlib packages are permitted).
# This guarantees the IPC adapter stays portable and never pulls in the
# orchestrator/executor/LLM/web side of the codebase.
fitness-memoryguard-isolation:
	@mg_deps=$$(go list -deps ./internal/memoryguard/...) || exit $$?; \
	mg_forbidden=$$(printf '%s\n' "$$mg_deps" | grep 'github.com/tkdtaylor/agent-builder/internal/' | grep -v 'agent-builder/internal/memoryguard' || true); \
	if [ -n "$$mg_forbidden" ]; then \
		echo "FAIL fitness-memoryguard-isolation: internal/memoryguard imports forbidden agent-builder/internal package(s):"; \
		printf '%s\n' "$$mg_forbidden"; \
		exit 1; \
	fi; \
	echo "PASS fitness-memoryguard-isolation: internal/memoryguard import graph contains no other agent-builder/internal packages."

fitness-diagrams-render:
	@python3 scripts/check-mermaid.py > /dev/null && \
	echo "PASS fitness-diagrams-render: all Mermaid blocks render on GitHub (no parse hazards)."

# fitness-no-self-repo-sink covers test-spec TC-085-05 (F-013, ADR 050 §2 /
# ADR 042 bright line): no registered recipe may declare the agent-builder
# own-repo (github.com/tkdtaylor/agent-builder) as a result sink / publish target.
# This is the STATIC half of the belt-and-suspenders self-repo guard; the RUNTIME
# half is the orchestrator's spawn-worker deny (decideSpawnWorker / targetsOwnRepo).
#
# It scans recipe source (internal/recipe, excluding _test.go and import lines) for
# the own-repo path appearing alongside a sink/remote/publish token on the same
# line. SELF_REPO_SINK_DIR overrides the scan root so the Go fixture test
# (TestTC085_05_FitnessCheckFiresOnViolation) can point it at a violation fixture
# and assert a non-zero exit.
SELF_REPO_SINK_DIR ?= internal/recipe
fitness-no-self-repo-sink:
	@hits=$$(grep -rnE 'github\.com/tkdtaylor/agent-builder' $(SELF_REPO_SINK_DIR) \
		--include='*.go' 2>/dev/null \
		| grep -v '_test\.go:' \
		| grep -vE ':[[:space:]]*//' \
		| grep -vE '^[^:]*:[0-9]+:[[:space:]]*"github\.com/tkdtaylor/agent-builder' \
		| grep -iE 'sink|remote|publish' || true); \
	if [ -n "$$hits" ]; then \
		echo "FAIL fitness-no-self-repo-sink: recipe source declares the agent-builder own-repo as a result sink / publish target (ADR 042 bright line):"; \
		printf '%s\n' "$$hits"; \
		exit 1; \
	fi; \
	echo "PASS fitness-no-self-repo-sink: no registered recipe targets the agent-builder own-repo as a result sink."

check: lint test fitness
	@echo "All checks passed."

# l6-preflight — operator-invoked host readiness check (NOT a gate prerequisite)
# Run on a provisioned host before L6 probe runs to confirm all prerequisites are met.
l6-preflight:
	bash scripts/l6-preflight.sh

# l6-probe — operator-invoked L6 evidence collector (NOT a gate prerequisite)
# Runs (or in --dry-run simulates) all 9 Phase 0 L6 probes in the prescribed
# closing order. Writes a structured evidence file paste-ready for coverage-tracker.md.
# Requires: make l6-preflight to return READY (or use --dry-run to bypass the gate).
l6-probe:
	bash scripts/l6-probe.sh
