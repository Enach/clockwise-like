.PHONY: test coverage

test:
	go test ./...

coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out
	@echo ""
	@go tool cover -func=coverage.out | tail -1
