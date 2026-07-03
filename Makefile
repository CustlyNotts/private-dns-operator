GOCACHE ?= $(CURDIR)/.cache/go-build
VERSION ?= v1.0.0
IMG ?= ghcr.io/custlynotts/private-dns-operator:$(VERSION)
LATEST_IMG ?= ghcr.io/custlynotts/private-dns-operator:latest

.PHONY: test
test:
	GOCACHE=$(GOCACHE) go test ./...

.PHONY: build
build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) go build -o bin/manager ./cmd

.PHONY: docker-build
docker-build:
	docker build -t $(IMG) .

.PHONY: docker-build-latest
docker-build-latest:
	docker build -t $(IMG) -t $(LATEST_IMG) .

.PHONY: docker-push
docker-push:
	docker push $(IMG)

.PHONY: docker-push-latest
docker-push-latest:
	docker push $(IMG)
	docker push $(LATEST_IMG)

.PHONY: release-check
release-check: test build
	@echo "release $(VERSION) is ready"
