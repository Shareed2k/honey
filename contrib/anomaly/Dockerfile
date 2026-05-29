FROM python:3.11-bullseye

ENV PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /tmp/anomaly
COPY contrib/anomaly/requirements.txt /tmp/anomaly/requirements.txt
RUN python -m pip install --upgrade pip \
    && python -m pip install -r /tmp/anomaly/requirements.txt

WORKDIR /work
