.PHONY: fmt vet test race verify-lock compose-config check-clean generate-provenance verify

fmt:
	gofmt -w cmd internal migrations tests

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

verify-lock:
	scripts/verify-pins/verify.sh --offline

compose-config:
	docker compose -f deploy/compose.yaml config --quiet

check-clean:
	scripts/check-clean-tree.sh

generate-provenance:
	scripts/verify-pins/build-provenance.sh --lock dependencies.lock.json --service gateway --output deploy/docker/generated/gateway-build-provenance.json
	scripts/verify-pins/build-provenance.sh --lock dependencies.lock.json --service pipeline --output deploy/docker/generated/pipeline-build-provenance.json

verify: fmt vet race verify-lock compose-config check-clean
	git diff --exit-code
