package zstd

/*
#include <stdlib.h>
#include "native.h"
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"unsafe"
)

const (
	errorBufferSize = 1024
	ExpectedVersion = "1.5.7"
)

// ProgressFunc reports processed and total bytes for one native operation.
type ProgressFunc func(processed, total uint64)

// Version returns the linked libzstd version.
func Version() string {
	return C.GoString(C.vipr_zstd_version())
}

// RequireExpectedVersion rejects builds linked to a different libzstd release.
func RequireExpectedVersion() error {
	if version := Version(); version != ExpectedVersion {
		return fmt.Errorf("viper-patcher requires libzstd %s, but is linked to %s", ExpectedVersion, version)
	}
	return nil
}

// CompressionLevelRange returns libzstd's accepted compression-level range.
func CompressionLevelRange() (int, int) {
	return int(C.vipr_zstd_min_level()), int(C.vipr_zstd_max_level())
}

// CompressFile creates a zstd patch-from frame from reference to target.
func CompressFile(referencePath, targetPath, outputPath string, level int, callback ProgressFunc) error {
	if err := RequireExpectedVersion(); err != nil {
		return err
	}
	minimum, maximum := CompressionLevelRange()
	if level < minimum || level > maximum {
		return fmt.Errorf("compression level %d is outside supported range %d..%d", level, minimum, maximum)
	}

	referenceCString := C.CString(referencePath)
	targetCString := C.CString(targetPath)
	outputCString := C.CString(outputPath)
	errorBuffer := C.calloc(1, C.size_t(errorBufferSize))
	defer C.free(unsafe.Pointer(referenceCString))
	defer C.free(unsafe.Pointer(targetCString))
	defer C.free(unsafe.Pointer(outputCString))
	if errorBuffer == nil {
		return fmt.Errorf("allocate native error buffer")
	}
	defer C.free(errorBuffer)

	handle := newProgressHandle(callback)
	defer handle.Delete()
	result := C.vipr_compress_file(
		referenceCString,
		targetCString,
		outputCString,
		C.int(level),
		C.uintptr_t(handle),
		(*C.char)(errorBuffer),
		errorBufferSize,
	)
	if result != 0 {
		return fmt.Errorf("zstd patch creation failed: %s", C.GoString((*C.char)(errorBuffer)))
	}
	return nil
}

// DecompressSegment applies one patch-from frame stored inside a VIPR container.
func DecompressSegment(referencePath, patchPath string, offset, length uint64, outputPath string, expectedOutputSize uint64, callback ProgressFunc) error {
	if err := RequireExpectedVersion(); err != nil {
		return err
	}
	referenceCString := C.CString(referencePath)
	patchCString := C.CString(patchPath)
	outputCString := C.CString(outputPath)
	errorBuffer := C.calloc(1, C.size_t(errorBufferSize))
	defer C.free(unsafe.Pointer(referenceCString))
	defer C.free(unsafe.Pointer(patchCString))
	defer C.free(unsafe.Pointer(outputCString))
	if errorBuffer == nil {
		return fmt.Errorf("allocate native error buffer")
	}
	defer C.free(errorBuffer)

	handle := newProgressHandle(callback)
	defer handle.Delete()
	result := C.vipr_decompress_segment(
		referenceCString,
		patchCString,
		C.uint64_t(offset),
		C.uint64_t(length),
		outputCString,
		C.uint64_t(expectedOutputSize),
		C.uintptr_t(handle),
		(*C.char)(errorBuffer),
		errorBufferSize,
	)
	if result != 0 {
		return fmt.Errorf("zstd patch application failed: %s", C.GoString((*C.char)(errorBuffer)))
	}
	return nil
}

func newProgressHandle(callback ProgressFunc) cgo.Handle {
	if callback == nil {
		callback = func(uint64, uint64) {}
	}
	return cgo.NewHandle(callback)
}

//export viprGoProgress
func viprGoProgress(handle C.uintptr_t, processed C.uint64_t, total C.uint64_t) {
	callback, ok := cgo.Handle(handle).Value().(ProgressFunc)
	if !ok {
		return
	}
	callback(uint64(processed), uint64(total))
}

var _ unsafe.Pointer
