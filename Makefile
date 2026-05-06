.PHONY: webui
webui:
	cd webui && npm ci && npm run build
