.PHONY: all build tidy fmt test race vet clean run

BINARY := mbox-summary
PACKAGE := ./cmd/mbox-summary

all: test build

tidy:
	go mod tidy

fmt:
	gofmt -w cmd internal

build: tidy
	go build -o $(BINARY) $(PACKAGE)

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

run: build
	@test -n "$(INPUT)" || (echo "INPUT is required: make run INPUT=mailbox.mbox OUTPUT=summary.html" && exit 1)
	./$(BINARY) --input $(INPUT) --output $(OUTPUT) --identity $(IDENTITY)

clean:
	rm -f $(BINARY)
