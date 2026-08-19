.PHONY: build install test hard vet check coverage clean

build:
	go build -o rta ./cmd/rta

# The gate is "install -> value < 60s", which needs something to install.
install:
	go install ./cmd/rta

test:
	go test ./...

# What CI should run, and what a change to shared state should be checked
# against: no cached results, no ordering luck, no data races.
hard:
	go test -count=1 -race -shuffle=on ./...

vet:
	go vet ./...

check: vet hard

# -coverpkg because most of internal/ is exercised from other packages'
# tests; without it the shared code reads as untested when it is not.
coverage:
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

clean:
	rm -f rta coverage.out

# Everything CI runs, so a green laptop means a green pipeline.
ci: vet hard coverage
	@test -z "$$(gofmt -l ./builtin ./cmd ./internal ./pkg)" || (echo "gofmt needed:"; gofmt -l ./builtin ./cmd ./internal ./pkg; exit 1)
