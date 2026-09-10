.PHONY: test lint build fixtures

test:
	go test ./...

lint:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...

build:
	go build ./...

# Regenerates test/testdata/{80,84} from Docker MySQL.
fixtures:
	./test/testdata/gen.sh
