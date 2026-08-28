.PHONY: lint
lint: ## Lint Code
	golangci-lint run --config .golangci.yml

.PHONY: vuln
vuln: ## Check for known vulnerabilities
	govulncheck ./...

.PHONY: unit
unit: ## Unit Tests
	go test -tags=unit $(shell go list ./...) $(TESTARGS)

.PHONY: run
run: ## Run the application
	go run ./cmd/${PLUGIN_NAME} --v 5 --node-name $(KUBE_NODE_NAME) --endpoint=unix:///csi/csi.sock --vdi-name-prefix=$(VDI_NAME_PREFIX) --cluster-tag=$(CLUSTER_TAGS) --xo-client-timeout=240s

.PHONY: mock
mock: ## Generate mocks
	go generate ./...
