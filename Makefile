.PHONY: webui openapi build-plugin-examples build-plugin-modules
build-plugin-examples:
	cd examples/plugins/echo && \
	  GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
	cp examples/plugins/echo/plugin.wasm internal/plugins/testdata/echo/plugin.wasm

build-plugin-modules:
	@for dir in bash shell copy template file service postgres; do \
	  echo "building plugins/$$dir"; \
	  (cd plugins/$$dir && GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .); \
	  mkdir -p examples/plugins/$$dir; \
	  cp plugins/$$dir/plugin.yaml plugins/$$dir/plugin.wasm examples/plugins/$$dir/; \
	done

webui:
	cd webui && npm ci && npm run build

# Regenerate OpenAPI 3 spec for the honey web API (swag + kin-openapi conversion).
openapi:
	cd internal/webserver && go generate .
