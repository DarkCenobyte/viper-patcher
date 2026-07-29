package zstd

/*
#include <stdlib.h>
#include "native.h"
*/
import "C"

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"runtime/cgo"
	"unsafe"

	"github.com/DarkCenobyte/viper-patcher/internal/zstdversion"
)

const (
	errorBufferSize = 1024
	ExpectedVersion = zstdversion.Version
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

// DecoderWindowLimit returns libzstd's normal maximum decoder window for the
// current target architecture.
func DecoderWindowLimit() uint64 {
	return uint64(C.vipr_zstd_decoder_window_limit())
}

// PreparedInput owns one positional patch handle that can prepare multiple
// bounded compressed segments. On Windows the handle is private and synchronous;
// on POSIX it reuses the caller's descriptor and native reads use pread.
type PreparedInput struct {
	owner   *os.File
	handle  uintptr
	release func()
}

// PrepareInput acquires one positional handle that remains valid until Close.
func PrepareInput(patch *os.File) (*PreparedInput, error) {
	if patch == nil {
		return nil, fmt.Errorf("patch file is required")
	}
	handle, release, err := acquirePositionalReadHandle(patch)
	if err != nil {
		return nil, err
	}
	return &PreparedInput{owner: patch, handle: handle, release: release}, nil
}

// Close releases the positional handle. It is safe to call more than once after
// every segment borrowing the input has completed.
func (input *PreparedInput) Close() error {
	if input == nil || input.release == nil {
		return nil
	}
	input.release()
	input.release = nil
	input.owner = nil
	input.handle = 0
	return nil
}

// Segment prepares one bounded compressed segment without reopening the input.
// The returned segment borrows input and must not outlive it.
func (input *PreparedInput) Segment(offset, length uint64) (*PreparedSegment, error) {
	if input == nil || input.owner == nil || input.release == nil {
		return nil, fmt.Errorf("prepared zstd input is closed")
	}
	if err := validateSegmentBounds(offset, length); err != nil {
		return nil, err
	}
	return &PreparedSegment{input: input, offset: offset, length: length}, nil
}

func validateSegmentBounds(offset, length uint64) error {
	if length == 0 {
		return fmt.Errorf("empty compressed segment")
	}
	if offset > math.MaxInt64 || length > math.MaxInt64-offset {
		return fmt.Errorf("compressed segment exceeds the supported signed 64-bit range")
	}
	return nil
}

// PreparedSegment describes one bounded compressed segment. It either borrows a
// reusable PreparedInput or owns a short-lived input created by PrepareSegment.
type PreparedSegment struct {
	input   *PreparedInput
	offset  uint64
	length  uint64
	release func()
}

// PrepareSegment acquires a short-lived positional input for one segment.
func PrepareSegment(patch *os.File, offset, length uint64) (*PreparedSegment, error) {
	if patch == nil {
		return nil, fmt.Errorf("patch file is required")
	}
	if err := validateSegmentBounds(offset, length); err != nil {
		return nil, err
	}
	input, err := PrepareInput(patch)
	if err != nil {
		return nil, err
	}
	segment := &PreparedSegment{input: input, offset: offset, length: length}
	segment.release = func() {
		_ = input.Close()
	}
	return segment, nil
}

// Close releases any positional input owned by this segment. Borrowed inputs
// remain available for subsequent segments.
func (segment *PreparedSegment) Close() error {
	if segment == nil || segment.input == nil {
		return nil
	}
	if segment.release != nil {
		segment.release()
	}
	segment.release = nil
	segment.input = nil
	return nil
}

// WindowSize inspects only the bounded zstd frame header.
func (segment *PreparedSegment) WindowSize() (uint64, error) {
	if segment == nil || segment.input == nil || segment.input.owner == nil || segment.input.release == nil {
		return 0, fmt.Errorf("prepared zstd segment is closed")
	}
	errorBuffer, err := nativeErrorBuffer()
	if err != nil {
		return 0, err
	}
	defer C.free(errorBuffer)

	var windowSize C.uint64_t
	result := C.vipr_zstd_frame_window_size(
		C.uintptr_t(segment.input.handle),
		C.uint64_t(segment.offset),
		C.uint64_t(segment.length),
		&windowSize,
		(*C.char)(errorBuffer),
		errorBufferSize,
	)
	runtime.KeepAlive(segment.input.owner)
	if result != 0 {
		return 0, fmt.Errorf("inspect zstd frame: %s", C.GoString((*C.char)(errorBuffer)))
	}
	return uint64(windowSize), nil
}

// FrameWindowSize inspects a frame through a short-lived prepared segment.
func FrameWindowSize(patch *os.File, offset, length uint64) (uint64, error) {
	segment, err := PrepareSegment(patch, offset, length)
	if err != nil {
		return 0, err
	}
	defer segment.Close()
	return segment.WindowSize()
}

func validateCompressionLevel(level int) error {
	if err := RequireExpectedVersion(); err != nil {
		return err
	}
	minimum, maximum := CompressionLevelRange()
	if level < minimum || level > maximum {
		return fmt.Errorf("compression level %d is outside supported range %d..%d", level, minimum, maximum)
	}
	return nil
}

func nativeErrorBuffer() (unsafe.Pointer, error) {
	buffer := C.calloc(1, C.size_t(errorBufferSize))
	if buffer == nil {
		return nil, fmt.Errorf("allocate native error buffer")
	}
	return buffer, nil
}

// CompressFile creates one standalone zstd frame from inputPath.
func CompressFile(inputPath, outputPath string, level int, callback ProgressFunc) error {
	if err := validateCompressionLevel(level); err != nil {
		return err
	}

	inputCString := C.CString(inputPath)
	outputCString := C.CString(outputPath)
	errorBuffer, err := nativeErrorBuffer()
	defer C.free(unsafe.Pointer(inputCString))
	defer C.free(unsafe.Pointer(outputCString))
	if err != nil {
		return err
	}
	defer C.free(errorBuffer)

	handle := newProgressHandle(callback)
	defer handle.Delete()
	result := C.vipr_compress_file(
		inputCString,
		outputCString,
		C.int(level),
		C.uintptr_t(handle),
		(*C.char)(errorBuffer),
		errorBufferSize,
	)
	if result != 0 {
		return fmt.Errorf("zstd compression failed: %s", C.GoString((*C.char)(errorBuffer)))
	}
	return nil
}

// CompressFileSegment creates one standalone zstd frame from a positional
// segment of an already-open file. The input cursor is never modified, so
// independent segments may be compressed concurrently from the same handle.
func CompressFileSegment(input *os.File, offset, length uint64, outputPath string, level int, callback ProgressFunc) error {
	if input == nil {
		return fmt.Errorf("compression input file is required")
	}
	if offset > math.MaxInt64 || length > math.MaxInt64-offset {
		return fmt.Errorf("compression input segment exceeds the supported signed 64-bit range")
	}
	if err := validateCompressionLevel(level); err != nil {
		return err
	}
	inputHandle, releaseInputHandle, err := acquirePositionalReadHandle(input)
	if err != nil {
		return err
	}
	defer releaseInputHandle()

	outputCString := C.CString(outputPath)
	errorBuffer, err := nativeErrorBuffer()
	defer C.free(unsafe.Pointer(outputCString))
	if err != nil {
		return err
	}
	defer C.free(errorBuffer)

	handle := newProgressHandle(callback)
	defer handle.Delete()
	result := C.vipr_compress_segment(
		C.uintptr_t(inputHandle),
		C.uint64_t(offset),
		C.uint64_t(length),
		outputCString,
		C.int(level),
		C.uintptr_t(handle),
		(*C.char)(errorBuffer),
		errorBufferSize,
	)
	// C receives only the numeric handle, so keep its Go owner reachable until
	// the native operation has finished using it.
	runtime.KeepAlive(input)
	if result != 0 {
		return fmt.Errorf("zstd segment compression failed: %s", C.GoString((*C.char)(errorBuffer)))
	}
	return nil
}

// Decoder reuses one native zstd context and its input/output buffers across
// multiple files. A Decoder is not safe for concurrent use.
type Decoder struct {
	native      *C.vipr_decoder
	windowLimit uint64
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
	return &Decoder{native: native, windowLimit: DecoderWindowLimit()}, nil
}

// SetWindowLimit caps the next and subsequent decode operations on this decoder.
// Decoder pools set it from the inspected frame before handing the decoder to a
// worker, closing the mutation window between reservation and decompression.
func (decoder *Decoder) SetWindowLimit(limit uint64) error {
	if decoder == nil || decoder.native == nil {
		return fmt.Errorf("zstd decoder is closed")
	}
	if limit == 0 || limit > DecoderWindowLimit() {
		return fmt.Errorf("zstd decoder window limit %d is invalid", limit)
	}
	decoder.windowLimit = limit
	return nil
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

// DecompressSegmentToFile decodes one standalone frame directly from an
// already-open patch handle to an already-open output handle. Output blocks are
// exposed synchronously so BLAKE3 can be calculated during the write pass.
func (decoder *Decoder) DecompressSegmentToFile(ctx context.Context, patch *os.File, offset, length uint64, output *os.File, expectedOutputSize uint64, callback ProgressFunc, outputCallback OutputFunc) error {
	if decoder == nil || decoder.native == nil {
		return fmt.Errorf("zstd decoder is closed")
	}
	segment, err := PrepareSegment(patch, offset, length)
	if err != nil {
		return err
	}
	defer segment.Close()
	return decoder.DecompressPreparedSegmentToFile(ctx, segment, output, expectedOutputSize, callback, outputCallback)
}

// DecompressPreparedSegmentToFile decodes one prepared segment without reopening
// its input handle.
func (decoder *Decoder) DecompressPreparedSegmentToFile(ctx context.Context, segment *PreparedSegment, output *os.File, expectedOutputSize uint64, callback ProgressFunc, outputCallback OutputFunc) error {
	return decoder.decompressSegment(ctx, segment, output, expectedOutputSize, callback, outputCallback)
}

// DecompressSegmentToWriter streams one standalone frame to writer without
// materializing an intermediate file. Memory use remains bounded by the native
// decoder buffers and the writer's own buffering.
func (decoder *Decoder) DecompressSegmentToWriter(ctx context.Context, patch *os.File, offset, length uint64, writer io.Writer, expectedOutputSize uint64, callback ProgressFunc) error {
	if writer == nil {
		return fmt.Errorf("decompressed output writer is required")
	}
	if decoder == nil || decoder.native == nil {
		return fmt.Errorf("zstd decoder is closed")
	}
	segment, err := PrepareSegment(patch, offset, length)
	if err != nil {
		return err
	}
	defer segment.Close()
	return decoder.DecompressPreparedSegmentToWriter(ctx, segment, writer, expectedOutputSize, callback)
}

// DecompressPreparedSegmentToWriter streams one prepared segment to writer.
func (decoder *Decoder) DecompressPreparedSegmentToWriter(ctx context.Context, segment *PreparedSegment, writer io.Writer, expectedOutputSize uint64, callback ProgressFunc) error {
	if writer == nil {
		return fmt.Errorf("decompressed output writer is required")
	}
	return decoder.decompressSegment(ctx, segment, nil, expectedOutputSize, callback, func(block []byte) error {
		written, err := writer.Write(block)
		if err == nil && written != len(block) {
			return io.ErrShortWrite
		}
		return err
	})
}

func (decoder *Decoder) decompressSegment(ctx context.Context, segment *PreparedSegment, output *os.File, expectedOutputSize uint64, callback ProgressFunc, outputCallback OutputFunc) error {
	if decoder == nil || decoder.native == nil {
		return fmt.Errorf("zstd decoder is closed")
	}
	if segment == nil || segment.input == nil || segment.input.owner == nil || segment.input.release == nil {
		return fmt.Errorf("prepared zstd segment is closed")
	}
	if output == nil && outputCallback == nil {
		return fmt.Errorf("decompressed output destination is required")
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

	var writeOutput C.int
	var outputHandle C.uintptr_t
	if output != nil {
		writeOutput = 1
		outputHandle = C.uintptr_t(output.Fd())
	}
	result := C.vipr_decoder_decompress_segment(
		decoder.native,
		C.uintptr_t(segment.input.handle),
		C.uint64_t(segment.offset),
		C.uint64_t(segment.length),
		writeOutput,
		outputHandle,
		C.uint64_t(expectedOutputSize),
		C.uint64_t(decoder.windowLimit),
		C.uintptr_t(handle),
		(*C.char)(errorBuffer),
		errorBufferSize,
	)
	// The cgo call only sees integer handles and cannot keep the owning files
	// reachable on the Go side while native code is reading or writing them.
	runtime.KeepAlive(segment.input.owner)
	runtime.KeepAlive(output)
	if callbacks.err != nil {
		return callbacks.err
	}
	if result != 0 {
		return fmt.Errorf("zstd decompression failed: %s", C.GoString((*C.char)(errorBuffer)))
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
