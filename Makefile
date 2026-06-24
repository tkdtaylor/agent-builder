.PHONY: lint format test fitness fitness-no-docker fitness-gate-blocking fitness-supervisor-isolation fitness-no-srt fitness-audit-isolation fitness-exec-sandbox-default fitness-policy-isolation check l6-preflight l6-probe

lint:
	golangci-lint run

format:
	gofmt -w .

test:
	go test ./...

# Fitness functions — see docs/spec/fitness-functions.md
fitness: fitness-no-docker fitness-gate-blocking fitness-supervisor-isolation fitness-no-srt fitness-audit-isolation fitness-exec-sandbox-default fitness-policy-isolation
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
