BINARY := redditwatch

.PHONY: build test lint run dry-run clean

build:
	go build -o $(BINARY) ./cmd/redditwatch

test:
	go test ./...

lint:
	gofmt -l . && go vet ./...

# Poll once and print what would be reported, without notifying or saving state.
dry-run:
	go run ./cmd/redditwatch -config config.json -once -dry-run -v

run:
	go run ./cmd/redditwatch -config config.json

clean:
	rm -f $(BINARY)
