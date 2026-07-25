#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SOURCE="$ROOT/third_party/zstd"
OUTPUT="$ROOT/build/zstd"
JOBS=${JOBS:-2}

if [ ! -f "$SOURCE/lib/zstd.h" ]; then
    printf 'zstd source is missing. Run scripts/fetch-zstd.sh first.\n' >&2
    exit 1
fi

make -C "$SOURCE/lib" clean >/dev/null
make -C "$SOURCE/lib" -j"$JOBS" libzstd.a \
    ZSTD_LEGACY_SUPPORT=0 \
    ZSTD_MULTITHREAD_SUPPORT=0 \
    MOREFLAGS="${MOREFLAGS:--O3}"

rm -rf "$OUTPUT"
mkdir -p "$OUTPUT/include" "$OUTPUT/lib"
cp "$SOURCE/lib/zstd.h" "$SOURCE/lib/zstd_errors.h" "$SOURCE/lib/zdict.h" "$OUTPUT/include/"
cp "$SOURCE/lib/libzstd.a" "$OUTPUT/lib/libzstd.a"
printf 'Built static libzstd at %s.\n' "$OUTPUT/lib/libzstd.a"
