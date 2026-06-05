.PHONY: lint format test fitness check

lint:
	golangci-lint run

format:
	gofmt -w .

test:
	go test ./...

# Fitness functions — see docs/spec/fitness-functions.md
# Empty umbrella by default — passes vacuously until rules are wired up.
fitness:
	@echo "No fitness functions defined yet. Add rules in docs/spec/fitness-functions.md and per-rule targets below."

check: lint test fitness
	@echo "All checks passed."
