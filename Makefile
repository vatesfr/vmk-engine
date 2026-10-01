.PHONY: lint
lint: ## Lint Code
	golangci-lint run --config .golangci.yml

.PHONY: pre-commit-install
pre-commit-install: ## Install the pre-commit and pre-push Git hooks
	pre-commit install --hook-type pre-commit --hook-type pre-push

.PHONY: pre-commit
pre-commit: ## Run every pre-commit hook on the repository
	pre-commit run --all-files

.PHONY: vuln
vuln: ## Check for known vulnerabilities
	govulncheck ./...

.PHONY: unit
unit: ## Unit Tests
	go test -tags=unit $(shell go list ./...) $(TESTARGS)

.PHONY: run
run: ## Run the application
	go run ./cmd/${PLUGIN_NAME} --v 5 --node-name $(KUBE_NODE_NAME) --endpoint=unix:///csi/csi.sock --vdi-name-prefix=$(VDI_NAME_PREFIX) --cluster-tag=$(CLUSTER_TAGS) --xo-client-timeout=240s

.PHONY: mocks
mocks: ## Generate mocks
	go generate ./...

.PHONY: run-local
run-local: ## Run the application locally
	go run ./cmd/http_server \
		--kube-client=kubeconfig \
		--kubeconfig="$(HOME)/.kube/config" \
		--namespace=default \
		--address=127.0.0.1 \
		--port=8080 \
		--swagger-ui=swagger
