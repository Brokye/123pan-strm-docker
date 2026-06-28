FROM python:3.11-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    DATA_DIR=/data \
    SETTINGS_PATH=/data/settings.yaml \
    CACHE_PATH=/data/cache.json \
    STRM_OUTPUT_DIR=/strm \
    HOST=0.0.0.0 \
    PORT=8000

WORKDIR /app

COPY requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt

COPY . /app

RUN mkdir -p /data /strm

EXPOSE 8000

CMD ["python", "strm_app.py"]
