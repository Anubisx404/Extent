import logging
import os

from flask import Flask, jsonify

app = Flask(__name__)
log = logging.getLogger(__name__)


@app.get("/health")
def health():
    log.info("health check")
    return jsonify(status="ok")


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=int(os.environ.get("PORT", "8080")))
