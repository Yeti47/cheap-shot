#!/usr/bin/env bash
# Headless import + auto-analysis of the de-interleaved firmware image.
# Re-runnable: pass -overwrite semantics by deleting the project directory.
set -euo pipefail

PROJECT_DIR=/work/projects
PROJECT=cheapshot
IMAGE=/work/firmware/flash_logical.bin

if [ ! -f "$IMAGE" ]; then
    echo "missing $IMAGE - run tools/decrc.py first" >&2
    exit 1
fi

exec analyzeHeadless "$PROJECT_DIR" "$PROJECT" \
    -import "$IMAGE" \
    -processor ARM:LE:32:v5t \
    -loader BinaryLoader \
    -loader-baseAddr 0x0 \
    -scriptPath /work/scripts \
    -preScript SetupBK7252.java \
    -analysisTimeoutPerFile 3600 \
    "$@"
