# examples/plugins/audio_transcribe/Dockerfile
FROM python:3.12-slim

WORKDIR /app

RUN pip install --no-cache-dir faster-whisper openai

COPY list_new.py /app/list_new.py
COPY process_file.py /app/process_file.py
