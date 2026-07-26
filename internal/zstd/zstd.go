package zstd

/*
#include <stdlib.h>
#include "native.h"
*/
import "C"

import (
	"context"
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

// OutputFunc consumes one decompressed output block synchronously. Implementations
// must not retain the supplied slice after returning.
type OutputFunc func([]byte) error

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

// CompressFile creates a zstd patch-from frame from reference to target. An
// empty reference file produces a regular standalone zstd frame.
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

// Decoder reuses one native zstd context and its input/output buffers across
// multiple files. A Decoder is not safe for concurrent use.
type Decoder struct {
	native *C.vipr_decoder
}

// NewDecoder creates a reusable native decoder.
func NewDecoder() (*Decoder, error) {
	if err := RequireExpectedVersion(); err != nil {
		return nil, err
	}
	native := C.vipr_decoder_create()
	if native == nil {
		return nil, fmt.Errorf("allocate native zstd decoder")
	}
	return &Decoder{native: native}, nil
}

// Close releases native decoder resources.
func (decoder *Decoder) Close() error {
	if decoder == nil || decoder.native == nil {
		return nil
	}
	C.vipr_decoder_free(decoder.native)
	decoder.native = nil
	return nil
}

type decodeCallbacks struct {
	ctx          context.Context
	progress     ProgressFunc
	output       OutputFunc
	lastReported uint64
	err          error
}

// DecompressSegmentToFile applies one frame directly from already-open source
// and patch handles to an already-open output handle. reference may be nil for
// standalone zstd payloads. Output blocks are delivered to outputCallback while
// still resident in the native buffer, allowing SHA-256 to be calculated during
// the write pass.
func (decoder *Decoder) DecompressSegmentToFile(ctx context.Context, reference, patch *os.File, offset, length uint64, output *os.File, expectedOutputSize uint64, callback ProgressFunc, outputCallback OutputFunc) error {
	if decoder == nil || decoder.native == nil {
		return fmt.Errorf("zstd decoder is closed")
	}
	if patch == nil || output == nil {
		return fmt.Errorf("patch and output files are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	callbacks := &decodeCallbacks{ctx: ctx, progress: callback, output: outputCallback}
	handle := cgo.NewHandle(callbacks)
	defer handle.Delete()
	errorBuffer := C.calloc(1, C.size_t(errorBufferSize))
	if errorBuffer == nil {
		return fmt.Errorf("allocate native error buffer")
	}
	defer C.free(errorBuffer)

	var referenceHandle C.uintptr_t
	if reference != nil {
		referenceHandle = C.uintptr_t(reference.Fd())
	}
	result := C.vipr_decoder_decompress_segment(
		decoder.native,
		referenceHandle,
		C.uintptr_t(patch.Fd()),
		C.uint64_t(offset),
		C.uint64_t(length),
		C.uintptr_t(output.Fd()),
		C.uint64_t(expectedOutputSize),
		C.uintptr_t(handle),
		(*C.char)(errorBuffer),
		errorBufferSize,
	)
	if callbacks.err != nil {
		return callbacks.err
	}
	if result != 0 {
		return fmt.Errorf("zstd patch application failed: %s", C.GoString((*C.char)(errorBuffer)))
	}
	return nil
}

// DecompressSegmentToFile preserves the previous path-based API for callers
// outside the patch package while routing through the reusable handle-based
// decoder.
func DecompressSegmentToFile(referencePath, patchPath string, offset, length uint64, output *os.File, expectedOutputSize uint64, callback ProgressFunc) error {
	if output == nil {
		return fmt.Errorf("patched output file is required")
	}
	var reference *os.File
	var err error
	if referencePath != "" {
		reference, err = os.Open(referencePath)
		if err != nil {
			return fmt.Errorf("open patch reference: %w", err)
		}
		defer reference.Close()
	}
	patch, err := os.Open(patchPath)
	if err != nil {
		return fmt.Errorf("open patch file: %w", err)
	}
	defer patch.Close()
	decoder, err := NewDecoder()
	if err != nil {
		return err
	}
	defer decoder.Close()
	return decoder.DecompressSegmentToFile(context.Background(), reference, patch, offset, length, output, expectedOutputSize, callback, nil)
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

//export viprGoOutput
func viprGoOutput(handle C.uintptr_t, data unsafe.Pointer, size C.uint64_t, processed C.uint64_t, total C.uint64_t) C.int {
	callbacks, ok := cgo.Handle(handle).Value().(*decodeCallbacks)
	if !ok {
		return 1
	}
	if err := callbacks.ctx.Err(); err != nil {
		callbacks.err = err
		return 1
	}
	if size > 0 && callbacks.output != nil {
		if uint64(size) > uint64(^uint(0)>>1) {
			callbacks.err = fmt.Errorf("native output block is too large")
			return 1
		}
		block := unsafe.Slice((*byte)(data), int(size))
		if err := callbacks.output(block); err != nil {
			callbacks.err = err
			return 1
		}
	}
	processedValue := uint64(processed)
	totalValue := uint64(total)
	if callbacks.progress != nil && (processedValue == totalValue || processedValue-callbacks.lastReported >= 8<<20) {
		callbacks.lastReported = processedValue
		callbacks.progress(processedValue, totalValue)
	}
	return 0
}
