test:
	@./go.test.sh
.PHONY: test

coverage:
	@./go.coverage.sh
.PHONY: coverage

test_fast:
	go test ./...
.PHONY: test_fast

fuzz:
	go test ./source/json/ -run xxx -fuzz FuzzParse -fuzztime 1m
	go test ./source/env/ -run xxx -fuzz FuzzParse -fuzztime 1m
.PHONY: fuzz

golden:
	go test ./schema/jsonschema/ -update
.PHONY: golden

example:
	go run ./examples/service
.PHONY: example

tidy:
	go mod tidy
.PHONY: tidy

lint:
	golangci-lint run --fix ./...
.PHONY: lint

fmt:
	golangci-lint fmt ./...
.PHONY: fmt
