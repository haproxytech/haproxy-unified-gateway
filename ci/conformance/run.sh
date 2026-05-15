#!/bin/sh
set -e
RESULTS_DIR="/tmp/results"
mkdir -p "${RESULTS_DIR}"

EXTRA_FLAGS=""
[ -n "${CONFORMANCE_RUN_TEST}" ]   && EXTRA_FLAGS="${EXTRA_FLAGS} -run-test ${CONFORMANCE_RUN_TEST}"
[ -n "${CONFORMANCE_SKIP_TESTS}" ] && EXTRA_FLAGS="${EXTRA_FLAGS} -skip-tests ${CONFORMANCE_SKIP_TESTS}"

CONFORMANCE_REPORT_OUTPUT="${RESULTS_DIR}/conformance-report.yaml" \
/gotestsum \
  --format standard-verbose \
  --raw-command \
  --junitfile "${RESULTS_DIR}/junit.xml" \
  -- go tool test2json -t /conformance.test \
    -test.v \
    -test.timeout 60m \
    ${EXTRA_FLAGS}
