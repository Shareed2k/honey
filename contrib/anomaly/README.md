# DistilBERT Log Anomaly Training

This folder contains a helper script to train a binary log anomaly classifier and export artifacts compatible with `honey logs --anomaly`.

## 1) Prepare data

Create CSV files with:

- `text`: raw log line or short window text
- `label`: `0` for normal, `1` for anomaly

Example:

```csv
text,label
INFO startup complete,0
ERROR database timeout after 30s,1
```

You can quickly test the workflow with:

- `contrib/anomaly/sample_train.csv`
- `contrib/anomaly/sample_eval.csv`

## 2) Install dependencies

```bash
python -m venv .venv
source .venv/bin/activate
pip install torch transformers datasets accelerate onnx
pip install -r contrib/anomaly/requirements.txt
```

### Optional: reusable Docker trainer image (recommended)

Build once:

```bash
make anomaly-docker-build
```

Then train repeatedly without reinstalling dependencies:

```bash
make anomaly-docker-train-sample
# or with custom files:
make anomaly-docker-train \
  ANOMALY_TRAIN_CSV=/path/to/train.csv \
  ANOMALY_EVAL_CSV=/path/to/eval.csv \
  ANOMALY_OUT_DIR=models
```

## 3) Train and export

```bash
python contrib/anomaly/train_and_export_distilbert.py \
  --train-csv contrib/anomaly/sample_train.csv \
  --eval-csv contrib/anomaly/sample_eval.csv \
  --out-dir ./models
```

Outputs:

- `./models/distilbert-log-anomaly.onnx`
- `./models/vocab.txt`

## 4) Validate in Honey

```bash
./honey logs "web-*" --anomaly --anomaly-selftest \
  --anomaly-model ./models/distilbert-log-anomaly.onnx \
  --anomaly-tokenizer ./models/vocab.txt
```

## 5) Run live

```bash
./honey logs "web-*" --unit nginx --follow \
  --anomaly \
  --anomaly-model ./models/distilbert-log-anomaly.onnx \
  --anomaly-tokenizer ./models/vocab.txt \
  --anomaly-threshold 0.90 \
  --anomaly-strict
```
