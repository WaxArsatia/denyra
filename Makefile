.PHONY: fmt fmt-check vet test race compose-config images verify

fmt:
	gofmt -w $$(find cmd internal migrations tests scripts -type f -name '*.go')

fmt-check:
	test -z "$$(gofmt -l $$(find cmd internal migrations tests scripts -type f -name '*.go'))"

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

compose-config:
	docker compose -f deploy/compose.yaml config --quiet

images:
	DENYRA_RELEASE_REFRESH=$$(date -u +%Y%m%dT%H%M%SZ) docker compose -f deploy/compose.yaml build --pull

verify: fmt-check vet race compose-config
