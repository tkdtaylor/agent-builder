.PHONY: lint format test fitness fitness-supervisor-isolation check

lint:
	golangci-lint run

format:
	gofmt -w .

test:
	go test ./...

# Fitness functions — see docs/spec/fitness-functions.md
fitness: fitness-supervisor-isolation
	@echo "Fitness checks passed."

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
