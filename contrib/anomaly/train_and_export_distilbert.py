#!/usr/bin/env python3
"""Train a DistilBERT log anomaly classifier and export ONNX + vocab.txt.

CSV format requirements:
- train file: columns `text`, `label`
- optional eval file: columns `text`, `label`
where label is 0 (normal) or 1 (anomaly).
"""

from __future__ import annotations

import argparse
import inspect
import os

import torch
from datasets import DatasetDict, load_dataset
from transformers import (
    AutoModelForSequenceClassification,
    AutoTokenizer,
    DataCollatorWithPadding,
    Trainer,
    TrainingArguments,
)


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--train-csv", required=True, help="Path to training CSV")
    p.add_argument("--eval-csv", default="", help="Optional eval CSV")
    p.add_argument("--model", default="distilbert/distilbert-base-uncased", help="HF model id")
    p.add_argument("--out-dir", required=True, help="Output directory")
    p.add_argument("--epochs", type=int, default=3)
    p.add_argument("--batch-size", type=int, default=16)
    p.add_argument("--lr", type=float, default=2e-5)
    p.add_argument("--max-len", type=int, default=128)
    p.add_argument("--seed", type=int, default=42)
    return p.parse_args()


def load_data(train_csv: str, eval_csv: str) -> DatasetDict:
    files = {"train": train_csv}
    if eval_csv:
        files["validation"] = eval_csv
    ds = load_dataset("csv", data_files=files)

    for split in ds:
        cols = set(ds[split].column_names)
        missing = {"text", "label"} - cols
        if missing:
            raise ValueError(f"{split} CSV missing columns: {sorted(missing)}")
    return ds


def export_onnx(model: AutoModelForSequenceClassification, out_path: str, max_len: int) -> None:
    model.eval()
    input_ids = torch.ones((1, max_len), dtype=torch.long)
    attention_mask = torch.ones((1, max_len), dtype=torch.long)

    class Wrapper(torch.nn.Module):
        def __init__(self, m):
            super().__init__()
            self.m = m

        def forward(self, input_ids, attention_mask):
            return self.m(input_ids=input_ids, attention_mask=attention_mask).logits

    wrapped = Wrapper(model)
    torch.onnx.export(
        wrapped,
        (input_ids, attention_mask),
        out_path,
        input_names=["input_ids", "attention_mask"],
        output_names=["logits"],
        dynamic_axes={
            "input_ids": {0: "batch", 1: "seq"},
            "attention_mask": {0: "batch", 1: "seq"},
            "logits": {0: "batch"},
        },
        opset_version=17,
    )


def main() -> None:
    args = parse_args()
    os.makedirs(args.out_dir, exist_ok=True)

    ds = load_data(args.train_csv, args.eval_csv)
    tokenizer = AutoTokenizer.from_pretrained(args.model)

    def tok(batch):
        return tokenizer(batch["text"], truncation=True, max_length=args.max_len)

    encoded = ds.map(tok, batched=True)
    encoded = encoded.rename_column("label", "labels")
    encoded.set_format(type="torch", columns=["input_ids", "attention_mask", "labels"])

    model = AutoModelForSequenceClassification.from_pretrained(args.model, num_labels=2)

    train_kw = dict(
        output_dir=os.path.join(args.out_dir, "checkpoints"),
        learning_rate=args.lr,
        per_device_train_batch_size=args.batch_size,
        per_device_eval_batch_size=args.batch_size,
        num_train_epochs=args.epochs,
        seed=args.seed,
        save_strategy="epoch" if "validation" in encoded else "no",
        logging_steps=50,
        report_to="none",
    )
    # transformers renamed this arg across versions:
    # - older: eval_strategy
    # - newer: evaluation_strategy
    sig = inspect.signature(TrainingArguments.__init__)
    if "evaluation_strategy" in sig.parameters:
        train_kw["evaluation_strategy"] = "epoch" if "validation" in encoded else "no"
    else:
        train_kw["eval_strategy"] = "epoch" if "validation" in encoded else "no"

    train_args = TrainingArguments(**train_kw)

    trainer = Trainer(
        model=model,
        args=train_args,
        train_dataset=encoded["train"],
        eval_dataset=encoded.get("validation"),
        data_collator=DataCollatorWithPadding(tokenizer=tokenizer),
    )

    trainer.train()

    onnx_path = os.path.join(args.out_dir, "distilbert-log-anomaly.onnx")
    export_onnx(trainer.model, onnx_path, args.max_len)

    tokenizer.save_pretrained(args.out_dir)
    trainer.model.save_pretrained(os.path.join(args.out_dir, "hf_model"))

    vocab_path = os.path.join(args.out_dir, "vocab.txt")
    if not os.path.exists(vocab_path):
        vocab = tokenizer.get_vocab()
        with open(vocab_path, "w", encoding="utf-8") as f:
            for token, _ in sorted(vocab.items(), key=lambda x: x[1]):
                f.write(token + "\n")

    print("Done.")
    print(f"ONNX model: {onnx_path}")
    print(f"Tokenizer vocab: {os.path.join(args.out_dir, 'vocab.txt')}")


if __name__ == "__main__":
    main()
