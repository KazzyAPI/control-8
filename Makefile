IMAGE ?= ghcr.io/kazzyapi/control-8:dev
COMPOSE = docker compose -f deploy/compose.yaml

.PHONY: build test test-k8s lint image up down kind-up kind-down
build:
	CGO_ENABLED=0 go build -o bin/control8 ./cmd/control8
test:
	go test ./...
test-k8s:
	KUBECONFIG=$(CURDIR)/tmp/kubeconfig go test -tags k8s ./...
lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (gofmt -l .; exit 1)
image:
	docker build -t $(IMAGE) .
up: image
	$(COMPOSE) up -d
down:
	$(COMPOSE) down
kind-up:
	scripts/kind-up.sh
kind-down:
	scripts/kind-down.sh
