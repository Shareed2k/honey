#!/usr/bin/env python3
"""Transcribes one audio file with faster-whisper, summarizes it via OpenAI,
writes a .done marker on success, and prints the result as JSON."""
import json
import os
import sys


def transcribe(path, model_size="base"):
    from faster_whisper import WhisperModel
    model = WhisperModel(model_size, device="cpu", compute_type="int8")
    segments, info = model.transcribe(path)
    text = " ".join(seg.text.strip() for seg in segments)
    return text, info.language


def summarize(text, model="gpt-4o-mini"):
    from openai import OpenAI
    client = OpenAI()
    resp = client.chat.completions.create(
        model=model,
        messages=[
            {"role": "system", "content": "Summarize this audio transcript concisely."},
            {"role": "user", "content": text},
        ],
    )
    return resp.choices[0].message.content


def process(filename, audio_dir, transcribe_fn=transcribe, summarize_fn=summarize):
    """Runs the full pipeline for one file and returns the result dict.
    transcribe_fn/summarize_fn are injectable for testing. The .done marker
    is only written after summarize_fn succeeds, so a failure at either
    stage leaves the file eligible for retry on the next scheduled run."""
    path = os.path.join(audio_dir, filename)

    text, language = transcribe_fn(path)
    summary = summarize_fn(text)

    marker = path + ".done"
    with open(marker, "w") as f:
        f.write("")

    return {
        "filename": filename,
        "language": language,
        "transcript": text,
        "summary": summary,
    }


def main():
    filename = sys.argv[1]
    audio_dir = os.environ.get("AUDIO_DIR", "/audio")
    result = process(filename, audio_dir)
    print(json.dumps(result))


if __name__ == "__main__":
    main()
