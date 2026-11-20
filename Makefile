IMAGE ?= ghcr.io/kazzyapi/control-8:dev
COMPOSE = docker compose -f deploy/compose.yaml

.PHONY: web web-clean web-test gen-client e2e build test test-k8s lint image up down kind-up kind-down
web:
	cd web && npm ci && npm run build
web-clean: # restore the committed stub (D-58) after a local `make web`
	git checkout web/dist/index.html && git clean -fdq web/dist/assets
web-test:
	cd web && npm run typecheck && npm test
gen-client:
	cd web && npm run gen-client
e2e: build
	cd web && npx playwright install chromium && npm run e2e
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
