.PHONY: webui openapi
webui:
	cd webui && npm ci && npm run build

# Regenerate OpenAPI 3 spec for the honey web API (swag + kin-openapi conversion).
openapi:
	cd internal/webserver && go generate .
