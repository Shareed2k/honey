.PHONY: webui openapi build-plugin-examples build-plugin-modules anomaly-install anomaly-download-model anomaly-train anomaly-train-sample anomaly-docker-train-sample anomaly-docker-build anomaly-docker-train

PYTHON ?= python3
ANOMALY_MODEL_ID ?= distilbert/distilbert-base-uncased
ANOMALY_BASE_DIR ?= models/distilbert-base-uncased
ANOMALY_OUT_DIR ?= models
ANOMALY_TRAIN_CSV ?= contrib/anomaly/sample_train.csv
ANOMALY_EVAL_CSV ?= contrib/anomaly/sample_eval.csv
ANOMALY_DOCKER_IMAGE ?= python:3.11-bullseye
ANOMALY_DOCKER_TRAINER_IMAGE ?= honey-anomaly-trainer:latest
build-plugin-examples:
	cd examples/plugins/echo && \
	  GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
	cp examples/plugins/echo/plugin.wasm internal/plugins/testdata/echo/plugin.wasm

build-plugin-modules:
	@for dir in bash shell copy template file service postgres rclone; do \
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

anomaly-install:
	$(PYTHON) -m pip install --upgrade pip
	$(PYTHON) -m pip install -r contrib/anomaly/requirements.txt

anomaly-download-model:
	hf download $(ANOMALY_MODEL_ID) --local-dir ./$(ANOMALY_BASE_DIR)

anomaly-train:
	$(PYTHON) contrib/anomaly/train_and_export_distilbert.py \
	  --train-csv $(ANOMALY_TRAIN_CSV) \
	  --eval-csv $(ANOMALY_EVAL_CSV) \
	  --out-dir ./$(ANOMALY_OUT_DIR)

anomaly-train-sample:
	$(MAKE) anomaly-train \
	  ANOMALY_TRAIN_CSV=contrib/anomaly/sample_train.csv \
	  ANOMALY_EVAL_CSV=contrib/anomaly/sample_eval.csv \
	  ANOMALY_OUT_DIR=models

anomaly-docker-train-sample:
	$(MAKE) anomaly-docker-train \
	  ANOMALY_TRAIN_CSV=contrib/anomaly/sample_train.csv \
	  ANOMALY_EVAL_CSV=contrib/anomaly/sample_eval.csv \
	  ANOMALY_OUT_DIR=models \
	  DOCKER_CONTEXT=remote-builder

anomaly-docker-build:
	DOCKER_CONTEXT=remote-builder docker build -t $(ANOMALY_DOCKER_TRAINER_IMAGE) -f contrib/anomaly/Dockerfile .

anomaly-docker-train:
	docker run --rm -it \
	  -v "$(PWD):/work" \
	  -w /work \
	  $(ANOMALY_DOCKER_TRAINER_IMAGE) \
	  bash -lc 'make anomaly-download-model && make anomaly-train ANOMALY_TRAIN_CSV=$(ANOMALY_TRAIN_CSV) ANOMALY_EVAL_CSV=$(ANOMALY_EVAL_CSV) ANOMALY_OUT_DIR=$(ANOMALY_OUT_DIR)'
