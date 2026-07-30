//go:build vipr_static_zstd

package nativev4

/*
#cgo CFLAGS: -DVIPR_STATIC_BLAKE3 -I${SRCDIR}/../../build/zstd/include -I${SRCDIR}/../../build/blake3/include
#cgo linux LDFLAGS: ${SRCDIR}/../../build/zstd/lib/libzstd.a ${SRCDIR}/../../build/blake3/lib/libblake3.a -pthread
#cgo darwin LDFLAGS: ${SRCDIR}/../../build/zstd/lib/libzstd.a ${SRCDIR}/../../build/blake3/lib/libblake3.a
#cgo windows LDFLAGS: ${SRCDIR}/../../build/zstd/lib/libzstd.a ${SRCDIR}/../../build/blake3/lib/libblake3.a
*/
import "C"
