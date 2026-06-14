# Minimal Makefile for golangci-lint (CI and local use). Day-to-day tasks use mage.

LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

GOLANGCI_LINT = $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)

GOLANGCI_LINT_VERSION_FILE := .golangci-lint-version
ifeq ($(wildcard $(GOLANGCI_LINT_VERSION_FILE)),)
$(error Missing $(GOLANGCI_LINT_VERSION_FILE).)
endif
GOLANGCI_LINT_VERSION ?= $(shell tr -d ' \r\n' < $(GOLANGCI_LINT_VERSION_FILE))

.PHONY: lint
lint: golangci-lint ## Run golangci-lint
	$(GOLANGCI_LINT) run --timeout 5m

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT)

$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv "$$(echo "$(1)" | sed "s/-$(3)$$//")" $(1) ;\
}
endef
