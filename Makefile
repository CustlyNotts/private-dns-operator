# ---- Versions -----------------------------------------------------------
# Pinned tool versions. Bump these deliberately; CI installs exactly these.
CONTROLLER_TOOLS_VERSION ?= v0.19.0
GOLANGCI_LINT_VERSION    ?= v2.6.0
KUSTOMIZE_VERSION        ?= v5.8.1

# ---- Release coordinates ------------------------------------------------
# A git tag vX.Y.Z yields image tag vX.Y.Z and chart version X.Y.Z.
VERSION          ?= v1.0.1
HELM_VERSION     ?= $(patsubst v%,%,$(VERSION))
HELM_APP_VERSION ?= $(VERSION)
IMAGE_REPO       ?= ghcr.io/custlynotts/private-dns-operator
IMG              ?= $(IMAGE_REPO):$(VERSION)
LATEST_IMG       ?= $(IMAGE_REPO):latest
HELM_OCI_REGISTRY ?= oci://ghcr.io/custlynotts/charts
HELM_CHART        ?= charts/private-dns-operator
HELM_PACKAGE_DIR  ?= dist/charts
INSTALL_MANIFEST  ?= dist/install.yaml

# ---- Paths --------------------------------------------------------------
GOCACHE     ?= $(CURDIR)/.cache/go-build
LOCALBIN    ?= $(CURDIR)/bin
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
GOLANGCI_LINT  ?= $(LOCALBIN)/golangci-lint
KUSTOMIZE      ?= $(LOCALBIN)/kustomize
GO          ?= GOCACHE=$(GOCACHE) go

.DEFAULT_GOAL := help

##@ General

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
	/^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } \
	/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: fmt
fmt: ## Format Go sources
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail if any Go source is not gofmt-clean
	@unformatted=$$(gofmt -l $$(git ls-files '*.go')); \
	if [ -n "$$unformatted" ]; then \
		echo "these files are not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: lint
lint: golangci-lint ## Run golangci-lint
	$(GOLANGCI_LINT) run --timeout 5m

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint with autofix
	$(GOLANGCI_LINT) run --fix --timeout 5m

.PHONY: test
test: ## Run unit tests with the race detector
	$(GO) test -race ./...

.PHONY: test-cover
test-cover: ## Run tests and write a coverage profile
	$(GO) test -race -coverprofile=cover.out -covermode=atomic ./...
	$(GO) tool cover -func=cover.out | tail -1

.PHONY: build
build: ## Build the manager binary into bin/
	mkdir -p $(LOCALBIN)
	$(GO) build -o $(LOCALBIN)/manager ./cmd

.PHONY: run
run: ## Run the manager against the current kubeconfig context
	$(GO) run ./cmd

##@ Code generation

.PHONY: generate
generate: controller-gen ## Regenerate deepcopy functions
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

.PHONY: manifests
manifests: controller-gen ## Regenerate CRDs from the API type markers
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=config/crd/bases

.PHONY: sync-crds
sync-crds: ## Copy the generated CRDs into the Helm chart
	rm -f $(HELM_CHART)/crds/*.yaml
	cp config/crd/bases/*.yaml $(HELM_CHART)/crds/

.PHONY: codegen
codegen: generate manifests sync-crds ## Run every generator

GENERATED_FILES = api/v1alpha1/zz_generated.deepcopy.go \
                  config/crd/bases \
                  $(HELM_CHART)/crds

.PHONY: verify-codegen
verify-codegen: ## Fail if generated files are out of date
# Compares the generators' output against what is checked in, rather than
# diffing against HEAD, so this is accurate with uncommitted work in the tree.
	@before=$$(find $(GENERATED_FILES) -type f | sort | xargs shasum); \
	$(MAKE) --no-print-directory codegen >/dev/null; \
	after=$$(find $(GENERATED_FILES) -type f | sort | xargs shasum); \
	if [ "$$before" != "$$after" ]; then \
		echo "generated files are out of date; run 'make codegen' and commit the result"; \
		echo "$$before" > /tmp/pdo-codegen-before; \
		echo "$$after" > /tmp/pdo-codegen-after; \
		diff /tmp/pdo-codegen-before /tmp/pdo-codegen-after || true; \
		exit 1; \
	fi; \
	echo "generated files are current"

##@ Verification

.PHONY: verify
verify: fmt-check vet lint test build verify-codegen helm-lint helm-template manifest-build ## Everything CI runs

##@ Manifests

.PHONY: manifest-build
manifest-build: kustomize ## Render config/default into dist/install.yaml
# Builds through a throwaway overlay in dist/ so the image override never
# leaves tracked files dirty.
	mkdir -p $(dir $(INSTALL_MANIFEST))
	rm -rf dist/kustomize && mkdir -p dist/kustomize
	cd dist/kustomize && \
		$(KUSTOMIZE) create --resources ../../config/default && \
		$(KUSTOMIZE) edit set image $(IMAGE_REPO)=$(IMG)
	$(KUSTOMIZE) build dist/kustomize > $(INSTALL_MANIFEST)
	rm -rf dist/kustomize
	@echo "wrote $(INSTALL_MANIFEST) for image $(IMG)"

.PHONY: install
install: kustomize ## Apply CRDs, RBAC, and the manager to the current cluster
	$(KUSTOMIZE) build config/default | kubectl apply --server-side -f -

.PHONY: uninstall
uninstall: kustomize ## Remove the operator from the current cluster
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found -f -

##@ Container images

.PHONY: docker-build
docker-build: ## Build the operator image
	docker build -t $(IMG) .

.PHONY: docker-build-latest
docker-build-latest: ## Build the operator image tagged with the version and latest
	docker build -t $(IMG) -t $(LATEST_IMG) .

.PHONY: docker-push
docker-push: ## Push the versioned operator image
	docker push $(IMG)

.PHONY: docker-push-latest
docker-push-latest: ## Push the versioned and latest operator images
	docker push $(IMG)
	docker push $(LATEST_IMG)

##@ Helm chart

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart
	helm lint $(HELM_CHART)

.PHONY: helm-template
helm-template: ## Render the Helm chart
	helm template private-dns-operator $(HELM_CHART) --namespace private-dns-system

.PHONY: helm-package
helm-package: ## Package the chart using the release version
	mkdir -p $(HELM_PACKAGE_DIR)
	helm package $(HELM_CHART) --version $(HELM_VERSION) --app-version $(HELM_APP_VERSION) --destination $(HELM_PACKAGE_DIR)

.PHONY: helm-push
helm-push: helm-package ## Package and push the chart to the OCI registry
	helm push $(HELM_PACKAGE_DIR)/private-dns-operator-$(HELM_VERSION).tgz $(HELM_OCI_REGISTRY)

##@ Release

.PHONY: set-version
set-version: ## Write a new release version everywhere: make set-version VERSION=v1.1.0
	hack/set-version.sh $(VERSION)

.PHONY: release-check
release-check: verify ## Confirm the tree is releasable
	@chart=$$(grep '^version:' $(HELM_CHART)/Chart.yaml | awk '{print $$2}'); \
	app=$$(grep '^appVersion:' $(HELM_CHART)/Chart.yaml | awk '{print $$2}'); \
	if [ "$$chart" != "$(HELM_VERSION)" ] || [ "$$app" != "$(VERSION)" ]; then \
		echo "Chart.yaml says version=$$chart appVersion=$$app, but VERSION=$(VERSION)."; \
		echo "Run 'make set-version VERSION=$(VERSION)' before tagging."; \
		exit 1; \
	fi
	@echo "release $(VERSION) is ready"

##@ Tooling

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Install controller-gen into bin/
$(CONTROLLER_GEN): $(LOCALBIN)
	@test -x $(CONTROLLER_GEN) && $(CONTROLLER_GEN) --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) GOCACHE=$(GOCACHE) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Install golangci-lint into bin/
$(GOLANGCI_LINT): $(LOCALBIN)
	@test -x $(GOLANGCI_LINT) && $(GOLANGCI_LINT) --version | grep -q $(patsubst v%,%,$(GOLANGCI_LINT_VERSION)) || \
	GOBIN=$(LOCALBIN) GOCACHE=$(GOCACHE) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Install kustomize into bin/
$(KUSTOMIZE): $(LOCALBIN)
	@test -x $(KUSTOMIZE) || \
	GOBIN=$(LOCALBIN) GOCACHE=$(GOCACHE) go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)

.PHONY: clean
clean: ## Remove build output
	rm -rf $(LOCALBIN) dist cover.out
