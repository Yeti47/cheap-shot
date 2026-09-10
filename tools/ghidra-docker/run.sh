#!/usr/bin/env bash
# Drop into the analysis container.  Firmware is mounted read-only; Ghidra
# projects land in ./ghidra_projects on the host (gitignored) so analysis
# survives between runs.
#
#   ./run.sh                     interactive shell
#   ./run.sh import              headless import + auto-analysis of the firmware
#   ./run.sh gui                 Ghidra GUI (needs X11)
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
image="cheapshot-ghidra"

if ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "building $image (first run, this takes a few minutes)..."
    docker build -t "$image" "$here"
fi

mkdir -p "$here/ghidra_projects"

args=(--rm -it
      -v "$repo/firmware/logical:/work/firmware:ro"
      -v "$here/ghidra_projects:/work/projects"
      -v "$here/scripts:/work/scripts:ro")

case "${1:-shell}" in
    gui)
        args+=(-e "DISPLAY=$DISPLAY" -v /tmp/.X11-unix:/tmp/.X11-unix)
        exec docker run "${args[@]}" "$image" ghidraRun
        ;;
    import)
        exec docker run "${args[@]}" "$image" /work/scripts/import.sh
        ;;
    *)
        exec docker run "${args[@]}" "$image" "$@"
        ;;
esac
