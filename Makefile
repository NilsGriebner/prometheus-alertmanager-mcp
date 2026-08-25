.PHONY: build helm-docs

build:
	go build -o alertmanager-mcp .

helm-docs:
	helm-docs --chart-search-root deployment/helm --template-files README.md.gotmpl