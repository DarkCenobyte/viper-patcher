#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SOURCE="$ROOT/third_party/blake3/c"
OUTPUT="$ROOT/build/blake3"
OBJECTS="$OUTPUT/obj"
CC=${CC:-cc}
AR=${AR:-ar}
RANLIB=${RANLIB:-ranlib}
CFLAGS=${BLAKE3_CFLAGS:--O3 -std=c11}

if [ ! -f "$SOURCE/blake3.h" ]; then
    printf 'BLAKE3 source is missing. Run scripts/fetch-blake3.sh first.\n' >&2
    exit 1
fi

rm -rf "$OUTPUT"
mkdir -p "$OBJECTS" "$OUTPUT/include" "$OUTPUT/lib"

target=$($CC -dumpmachine 2>/dev/null || printf unknown)
# -dumpmachine reports the compiler installation target and may still say
# x86_64 when a wrapper adds -m32. Preprocessor macros describe the ABI that
# will actually be emitted and are therefore authoritative for Linux 386.
# shellcheck disable=SC2086
compiler_macros=$(printf '' | $CC $CFLAGS -dM -E - 2>/dev/null || true)
if printf '%s\n' "$compiler_macros" | grep -q '^#define __i386__ 1$'; then
    architecture=x86
elif printf '%s\n' "$compiler_macros" | grep -q '^#define __x86_64__ 1$'; then
    architecture=x86_64
elif printf '%s\n' "$compiler_macros" | grep -Eq '^#define (__aarch64__|__arm64__) 1$'; then
    architecture=arm64
else
    case "$target" in
        i?86*) architecture=x86 ;;
        x86_64*|amd64*) architecture=x86_64 ;;
        aarch64*|arm64*) architecture=arm64 ;;
        *) architecture=other ;;
    esac
fi

common_defs=""
extra_sources=""
case "$architecture:$target" in
    x86_64:*w64*|x86_64:*mingw*)
        common_defs="-DIS_X86"
        extra_sources="blake3_sse2_x86-64_windows_gnu.S: blake3_sse41_x86-64_windows_gnu.S: blake3_avx2_x86-64_windows_gnu.S: blake3_avx512_x86-64_windows_gnu.S:-mavx512f,-mavx512vl"
        ;;
    x86_64:*)
        common_defs="-DIS_X86"
        extra_sources="blake3_sse2_x86-64_unix.S: blake3_sse41_x86-64_unix.S: blake3_avx2_x86-64_unix.S: blake3_avx512_x86-64_unix.S:-mavx512f,-mavx512vl"
        ;;
    x86:*)
        common_defs="-DIS_X86"
        extra_sources="blake3_sse2.c:-fno-lto,-msse2 blake3_sse41.c:-fno-lto,-msse4.1 blake3_avx2.c:-fno-lto,-mavx2 blake3_avx512.c:-fno-lto,-mavx512f,-mavx512vl"
        ;;
    arm64:*)
        common_defs="-DBLAKE3_USE_NEON=1"
        extra_sources="blake3_neon.c:-fno-lto"
        ;;
    *)
        common_defs="-DBLAKE3_NO_SSE2 -DBLAKE3_NO_SSE41 -DBLAKE3_NO_AVX2 -DBLAKE3_NO_AVX512"
        ;;
esac

objects=""
for source in blake3.c blake3_dispatch.c blake3_portable.c; do
    object="$OBJECTS/${source%.c}.o"
    # shellcheck disable=SC2086
    $CC $CFLAGS $common_defs -I"$SOURCE" -c "$SOURCE/$source" -o "$object"
    objects="$objects $object"
done
for item in $extra_sources; do
    source=${item%%:*}
    flags=${item#*:}
    flags=$(printf '%s' "$flags" | tr ',' ' ')
    object="$OBJECTS/${source%.c}.o"
    # shellcheck disable=SC2086
    $CC $CFLAGS $common_defs $flags -I"$SOURCE" -c "$SOURCE/$source" -o "$object"
    objects="$objects $object"
done
# shellcheck disable=SC2086
$AR rcs "$OUTPUT/lib/libblake3.a" $objects
$RANLIB "$OUTPUT/lib/libblake3.a" 2>/dev/null || true
cp "$SOURCE/blake3.h" "$OUTPUT/include/"
printf 'Built official SIMD-dispatched BLAKE3 at %s.\n' "$OUTPUT/lib/libblake3.a"
