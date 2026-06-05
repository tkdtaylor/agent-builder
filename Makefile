.PHONY: lint format test fitness fitness-no-docker fitness-supervisor-isolation check

lint:
	golangci-lint run

format:
	gofmt -w .

test:
	go test ./...

# Fitness functions — see docs/spec/fitness-functions.md
fitness: fitness-no-docker fitness-supervisor-isolation
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
