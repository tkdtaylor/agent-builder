.PHONY: lint format test fitness fitness-no-docker fitness-gate-blocking fitness-supervisor-isolation check

lint:
	golangci-lint run

format:
	gofmt -w .

test:
	go test ./...

# Fitness functions — see docs/spec/fitness-functions.md
fitness: fitness-no-docker fitness-gate-blocking fitness-supervisor-isolation
	@echo "Fitness checks passed."

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
			! -path './docs/architecture/*.md' \
			! -path './docs/architecture/decisions/*' \
			! -path './docs/spec/SPEC.md' \
			! -path './docs/spec/fitness-functions.md' \
			! -path './AGENTS.md' \
			! -path './CLAUDE.md' \
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

check: lint test fitness
	@echo "All checks passed."
