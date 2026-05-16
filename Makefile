WORKFLOW   ?= .github/workflows/all.yml
K6_CI_REF  := $(shell grep -oE 'grafana/k6-ci/[^@[:space:]]+@[A-Za-z0-9._/-]+' $(WORKFLOW) | head -n1 | cut -d@ -f2)
BASE_URL   := https://raw.githubusercontent.com/grafana/k6-ci/$(K6_CI_REF)/.golangci.yml

LINT_DIR   ?= build/lint
LINT_BASE  := $(LINT_DIR)/.golangci-base.yml
LINT_FINAL := $(LINT_DIR)/.golangci.yml
LINT_PATCH ?= .golangci.patch

all: lint test

$(LINT_DIR):
	mkdir -p $@

$(LINT_BASE): $(WORKFLOW) | $(LINT_DIR)
	curl -fsSL $(BASE_URL) -o $@

$(LINT_FINAL): $(LINT_BASE) $(wildcard $(LINT_PATCH))
	cp $(LINT_BASE) $@
	@if [ -f $(LINT_PATCH) ]; then \
	  echo "Applying $(LINT_PATCH)"; \
	  git apply --directory=$(LINT_DIR) $(LINT_PATCH); \
	fi

## lint: Run golangci-lint with the k6-ci config (and optional .golangci.patch).
.PHONY: lint
lint: $(LINT_FINAL)
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$$(head -n1 $(LINT_BASE) | tr -d '# ') \
	  run --config=$(LINT_FINAL) ./...

## update-lint-patch: Regenerate .golangci.patch from the locally edited $(LINT_FINAL).
.PHONY: update-lint-patch
update-lint-patch: $(LINT_BASE)
	@if [ ! -f $(LINT_FINAL) ]; then \
	  echo "Run 'make lint' first to materialize $(LINT_FINAL), edit it, then re-run."; \
	  exit 1; \
	fi
	-diff -u --label a/.golangci.yml --label b/.golangci.yml $(LINT_BASE) $(LINT_FINAL) > $(LINT_PATCH)

## clean-lint: Remove $(LINT_DIR).
.PHONY: clean-lint
clean-lint:
	rm -rf $(LINT_DIR)

.PHONY: test
test:
	go test -race  ./...

.PHONY: readme
readme:
	go run ./tools/gendoc README.md
