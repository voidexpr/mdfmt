.PHONY: all build test test-race vet lint fmt generate-check vulncheck tidy-check install clean coverage ci screenshots

all: ci

build:
	go build -o mdfmt .

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

lint:
	$(shell go env GOPATH)/bin/staticcheck ./...

fmt:
	go fmt ./...

generate-check:
	go generate ./internal/mdhighlight
	git diff --exit-code -- assets/syntax.css

vulncheck:
	$(shell go env GOPATH)/bin/govulncheck ./...

tidy-check:
	go mod tidy -diff

install:
	go install honnef.co/go/tools/cmd/staticcheck@2025.1.1
	go install golang.org/x/vuln/cmd/govulncheck@v1.1.4

clean:
	rm -f mdfmt cover.out

coverage:
	go test -coverpkg=./... -coverprofile=cover.out ./...
	go tool cover -func=cover.out

screenshots: build
	sh docs/take-screenshots.sh

ci: build test-race vet lint generate-check tidy-check vulncheck
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
