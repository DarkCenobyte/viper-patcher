package zstd

/*
#include <stdlib.h>
#include "native.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
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
func DecompressSegment(referencePath, patchPath string, offset, length uint64, outputPath string, expectedOutputSize uint64, callback ProgressFunc) (resultError error) {
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open patched output: %w", err)
	}
	defer func() {
		resultError = errors.Join(resultError, wrapFileError("close patched output", output.Close()))
	}()
	if err := DecompressSegmentToFile(referencePath, patchPath, offset, length, output, expectedOutputSize, callback); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("sync patched output: %w", err)
	}
	return nil
}

// DecompressSegmentToFile applies one patch-from frame to an already-open file.
// Keeping the handle open prevents another process from replacing the output
// path between secure creation and native decompression.
func DecompressSegmentToFile(referencePath, patchPath string, offset, length uint64, output *os.File, expectedOutputSize uint64, callback ProgressFunc) error {
	if output == nil {
		return fmt.Errorf("patched output file is required")
	}
	if err := RequireExpectedVersion(); err != nil {
		return err
	}
	referenceCString := C.CString(referencePath)
	patchCString := C.CString(patchPath)
	errorBuffer := C.calloc(1, C.size_t(errorBufferSize))
	defer C.free(unsafe.Pointer(referenceCString))
	defer C.free(unsafe.Pointer(patchCString))
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
		C.uintptr_t(output.Fd()),
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

func wrapFileError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
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
