.PHONY: fmt fmt-check vet test race compose-config images verify acceptance live-compatibility

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

acceptance:
	DENYRA_ACCEPTANCE_COMPOSE=1 go test ./tests/acceptance -count=1

live-compatibility:
	DENYRA_LIVE_COMPATIBILITY=1 go test ./tests/integration/pipeline -run '^TestLiveCompatibility$$' -count=1

verify: fmt-check vet race compose-config
