GOCACHE ?= $(CURDIR)/.cache/go-build
VERSION ?= v1.0.1
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

HELM_CHART ?= charts/private-dns-operator
HELM_PACKAGE_DIR ?= dist/charts

.PHONY: helm-lint
helm-lint:
	helm lint $(HELM_CHART)

.PHONY: helm-template
helm-template:
	helm template private-dns-operator $(HELM_CHART) --namespace private-dns-system

.PHONY: helm-package
helm-package:
	mkdir -p $(HELM_PACKAGE_DIR)
	helm package $(HELM_CHART) --destination $(HELM_PACKAGE_DIR)
