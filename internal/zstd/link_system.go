//go:build vipr_legacy_zstd && !vipr_static_zstd

package zstd

/*
#cgo pkg-config: libzstd
*/
import "C"
