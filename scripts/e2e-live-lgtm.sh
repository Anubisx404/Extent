#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RAW_BIN="${1:-./bin/extent}"
if [[ "${RAW_BIN}" != /* ]]; then
  RAW_BIN="${REPO_ROOT}/${RAW_BIN}"
fi
EXTENT_BIN="$(cd "$(dirname "${RAW_BIN}")" && pwd)/$(basename "${RAW_BIN}")"

if [[ ! -x "${EXTENT_BIN}" ]]; then
  echo "error: extent binary not found or not executable: ${EXTENT_BIN}" >&2
  exit 1
fi

TMP_ROOT="$(mktemp -d)"
STACK_DIR="${TMP_ROOT}/stack"
ARTIFACTS_DIR="${TMP_ROOT}/artifacts"
APP_PID=""

stop_app() {
  local pid="${1:-}"
  if [[ -z "${pid}" ]]; then
    return 0
  fi
  if kill -0 "${pid}" 2>/dev/null; then
    pkill -P "${pid}" 2>/dev/null || true
    kill "${pid}" 2>/dev/null || true
    for _ in $(seq 1 10); do
      if ! kill -0 "${pid}" 2>/dev/null; then
        break
      fi
      sleep 0.5
    done
    if kill -0 "${pid}" 2>/dev/null; then
      pkill -9 -P "${pid}" 2>/dev/null || true
      kill -9 "${pid}" 2>/dev/null || true
    fi
  fi
  wait "${pid}" 2>/dev/null || true
}

cleanup() {
  set +e
  stop_app "${APP_PID}"
  APP_PID=""
  if [[ -n "${STACK_DIR}" && -f "${STACK_DIR}/docker-compose.observability.yml" ]]; then
    "${EXTENT_BIN}" stack down "${STACK_DIR}" >/dev/null 2>&1 || true
    docker compose -f "${STACK_DIR}/docker-compose.observability.yml" down -v >/dev/null 2>&1 || true
  fi
  if [[ -n "${TMP_ROOT}" && -d "${TMP_ROOT}" ]]; then
    rm -rf "${TMP_ROOT}"
  fi
}
trap cleanup EXIT

mkdir -p "${STACK_DIR}" "${ARTIFACTS_DIR}"

echo "==> Starting shared LGTM stack in ${STACK_DIR}"
"${EXTENT_BIN}" apply --force "${STACK_DIR}"
"${EXTENT_BIN}" stack up --wait --timeout 3m "${STACK_DIR}"
"${EXTENT_BIN}" stack status --json "${STACK_DIR}"

FIXTURES=(node-cjs node-esm python-flask go-nethttp)

for fixture in "${FIXTURES[@]}"; do
  echo "==> Running live E2E fixture: ${fixture}"
  FIXTURE_SRC="${REPO_ROOT}/testdata/e2e/${fixture}"
  FIXTURE_DIR="${TMP_ROOT}/fixtures/${fixture}"
  APP_LOG="${TMP_ROOT}/${fixture}.log"

  mkdir -p "${FIXTURE_DIR}"
  cp -R "${FIXTURE_SRC}/." "${FIXTURE_DIR}/"

  "${EXTENT_BIN}" scan "${FIXTURE_DIR}"
  "${EXTENT_BIN}" apply --force "${FIXTURE_DIR}"
  "${EXTENT_BIN}" instrument --mode bootstrap --apply "${FIXTURE_DIR}"

  case "${fixture}" in
    node-cjs|node-esm)
      "${EXTENT_BIN}" deps --install "${FIXTURE_DIR}"
      (
        cd "${FIXTURE_DIR}"
        exec env \
          PORT=8080 \
          OTEL_SERVICE_NAME="${fixture}" \
          OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318" \
          OTEL_EXPORTER_OTLP_PROTOCOL="http/protobuf" \
          OTEL_METRIC_EXPORT_INTERVAL="2000" \
          npm start
      ) > "${APP_LOG}" 2>&1 &
      APP_PID=$!
      ;;
    python-flask)
      python3 -m venv "${FIXTURE_DIR}/.venv"
      "${FIXTURE_DIR}/.venv/bin/python" -m pip install -r "${FIXTURE_DIR}/requirements.txt"
      (
        export VIRTUAL_ENV="${FIXTURE_DIR}/.venv"
        export PATH="${FIXTURE_DIR}/.venv/bin:${PATH}"
        "${EXTENT_BIN}" deps --install "${FIXTURE_DIR}"
      )
      (
        cd "${FIXTURE_DIR}"
        exec env \
          PORT=8080 \
          OTEL_SERVICE_NAME="${fixture}" \
          OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318" \
          OTEL_EXPORTER_OTLP_PROTOCOL="http/protobuf" \
          OTEL_METRIC_EXPORT_INTERVAL="2000" \
          "${FIXTURE_DIR}/.venv/bin/python" app.py
      ) > "${APP_LOG}" 2>&1 &
      APP_PID=$!
      ;;
    go-nethttp)
      "${EXTENT_BIN}" deps --install "${FIXTURE_DIR}"
      (
        cd "${FIXTURE_DIR}"
        go build -o app .
      )
      (
        cd "${FIXTURE_DIR}"
        exec env \
          PORT=8080 \
          OTEL_SERVICE_NAME="${fixture}" \
          OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318" \
          OTEL_EXPORTER_OTLP_PROTOCOL="http/protobuf" \
          OTEL_METRIC_EXPORT_INTERVAL="2000" \
          ./app
      ) > "${APP_LOG}" 2>&1 &
      APP_PID=$!
      ;;
    *)
      echo "error: unknown fixture: ${fixture}" >&2
      exit 1
      ;;
  esac

  READY=0
  for _ in $(seq 1 30); do
    if ! kill -0 "${APP_PID}" 2>/dev/null; then
      echo "error: ${fixture} exited before becoming ready" >&2
      cat "${APP_LOG}" >&2 || true
      exit 1
    fi
    STATUS_CODE="$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health || true)"
    if [[ "${STATUS_CODE}" == "200" ]]; then
      READY=1
      break
    fi
    sleep 1
  done

  if [[ "${READY}" != "1" ]]; then
    echo "error: ${fixture} failed to return HTTP 200 on http://127.0.0.1:8080/health within 30s" >&2
    cat "${APP_LOG}" >&2 || true
    exit 1
  fi

  "${EXTENT_BIN}" smoke --url http://127.0.0.1:8080/health --service "${fixture}" --requests 5 --json
  "${EXTENT_BIN}" verify --url http://127.0.0.1:8080/health --service "${fixture}" --grafana http://localhost:3000 "${STACK_DIR}"
  "${EXTENT_BIN}" report --service "${fixture}" --tempo http://localhost:3200 --loki http://localhost:3100 --format html --output "${ARTIFACTS_DIR}/${fixture}-report.html" "${FIXTURE_DIR}"
  "${EXTENT_BIN}" score --service "${fixture}" --tempo http://localhost:3200 --loki http://localhost:3100 --json

  if [[ ! -s "${ARTIFACTS_DIR}/${fixture}-report.html" ]]; then
    echo "error: missing or empty HTML report for ${fixture}" >&2
    exit 1
  fi

  stop_app "${APP_PID}"
  APP_PID=""

  if [[ "${fixture}" != "go-nethttp" ]]; then
    "${EXTENT_BIN}" instrument --undo "${FIXTURE_DIR}"
  fi
  echo "==> Completed live E2E fixture: ${fixture}"
done

echo "==> All 4 live E2E fixtures passed"
