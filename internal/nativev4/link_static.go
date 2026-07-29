//go:build vipr_static_zstd

package nativev4

/*
#cgo CFLAGS: -I${SRCDIR}/../../build/zstd/include
#cgo linux LDFLAGS: ${SRCDIR}/../../build/zstd/lib/libzstd.a -pthread
#cgo darwin LDFLAGS: ${SRCDIR}/../../build/zstd/lib/libzstd.a
#cgo windows LDFLAGS: ${SRCDIR}/../../build/zstd/lib/libzstd.a
*/
import "C"
