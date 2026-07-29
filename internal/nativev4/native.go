package nativev4

/*
#include <stdlib.h>
#include "native.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

const errorBufferSize = 1024

type Status int

const (
	StatusOK Status = iota
	StatusCancelled
	StatusInvalidArgument
	StatusInvalidWindow
	StatusSourceMismatch
	StatusOutputMismatch
	StatusReadError
	StatusWriteError
	StatusZstdError
	StatusMemoryLimit
	StatusUnsupported
	StatusInternal
)

type NativeError struct {
	Status Status
	Detail string
}

func (e *NativeError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("native V4 operation failed: %s", statusName(e.Status))
	}
	return fmt.Sprintf("native V4 operation failed (%s): %s", statusName(e.Status), e.Detail)
}
func statusName(status Status) string { return C.GoString(C.vipr_status_name(C.vipr_status(status))) }
func IsUnsupported(err error) bool {
	var native *NativeError
	return errors.As(err, &native) && native.Status == StatusUnsupported
}
func IsSourceMismatch(err error) bool {
	var native *NativeError
	return errors.As(err, &native) && native.Status == StatusSourceMismatch
}

func checkStatus(status C.vipr_status, errorBuffer unsafe.Pointer) error {
	if status == C.VIPR_STATUS_OK {
		return nil
	}
	detail := ""
	if errorBuffer != nil {
		detail = C.GoString((*C.char)(errorBuffer))
	}
	if status == C.VIPR_STATUS_CANCELLED {
		return context.Canceled
	}
	return &NativeError{Status: Status(status), Detail: detail}
}

func withErrorBuffer(call func(unsafe.Pointer) C.vipr_status) error {
	buffer := C.calloc(1, errorBufferSize)
	if buffer == nil {
		return fmt.Errorf("allocate native error buffer")
	}
	defer C.free(buffer)
	return checkStatus(call(buffer), buffer)
}

type Session struct {
	native *C.vipr_io_session
	source *os.File
	patch  *os.File
	output *os.File
}

type IOProfile uint8

const (
	IOAuto IOProfile = iota
	IOHDD
	IOSSD
	IONVMe
)

func NewSession(source, patch, output *os.File) (*Session, error) {
	return NewSessionWithProfile(source, patch, output, IOAuto)
}

func NewSessionWithProfile(source, patch, output *os.File, profile IOProfile) (*Session, error) {
	if profile > IONVMe {
		return nil, fmt.Errorf("invalid native I/O profile %d", profile)
	}
	raw := func(file *os.File) C.uintptr_t {
		if file == nil {
			return 0
		}
		return C.uintptr_t(file.Fd())
	}
	buffer := C.calloc(1, errorBufferSize)
	if buffer == nil {
		return nil, fmt.Errorf("allocate native error buffer")
	}
	defer C.free(buffer)
	native := C.vipr_session_create(raw(source), raw(patch), raw(output), boolInt(source != nil), boolInt(patch != nil), boolInt(output != nil), C.int(profile), (*C.char)(buffer), errorBufferSize)
	if native == nil {
		return nil, &NativeError{Status: StatusInternal, Detail: C.GoString((*C.char)(buffer))}
	}
	return &Session{native: native, source: source, patch: patch, output: output}, nil
}
func boolInt(value bool) C.int {
	if value {
		return 1
	}
	return 0
}
func (s *Session) Close() error {
	if s == nil || s.native == nil {
		return nil
	}
	C.vipr_session_free(s.native)
	s.native = nil
	s.source = nil
	s.patch = nil
	s.output = nil
	return nil
}

func ZstdVersion() string { return C.GoString(C.vipr_zstd_version()) }

func HashBytes(data []byte) (patchformat.Digest, error) {
	var result patchformat.Digest
	var pointer *C.uint8_t
	if len(data) != 0 {
		pointer = (*C.uint8_t)(unsafe.Pointer(&data[0]))
	}
	status := C.vipr_hash_bytes(pointer, C.size_t(len(data)), (*C.uint8_t)(unsafe.Pointer(&result[0])))
	runtime.KeepAlive(data)
	return result, checkStatus(status, nil)
}

func TreeRoot(size, chunkSize uint64, digests []patchformat.Digest) (patchformat.Digest, error) {
	var result patchformat.Digest
	var pointer *C.uint8_t
	if len(digests) != 0 {
		pointer = (*C.uint8_t)(unsafe.Pointer(&digests[0][0]))
	}
	status := C.vipr_tree_root(C.uint64_t(size), C.uint64_t(chunkSize), pointer, C.uint32_t(len(digests)), (*C.uint8_t)(unsafe.Pointer(&result[0])))
	runtime.KeepAlive(digests)
	return result, checkStatus(status, nil)
}

func (s *Session) HashFileTree(ctx context.Context, usePatch bool, size, chunkSize uint64) (patchformat.Digest, []patchformat.Digest, error) {
	if s == nil || s.native == nil {
		return patchformat.Digest{}, nil, fmt.Errorf("native session is unavailable")
	}
	count := 0
	if size != 0 {
		count = int((size + chunkSize - 1) / chunkSize)
	}
	digests := make([]patchformat.Digest, count)
	var root patchformat.Digest
	cancel, stop := newCancelWord(ctx)
	defer stop()
	var pointer *C.uint8_t
	if len(digests) != 0 {
		pointer = (*C.uint8_t)(unsafe.Pointer(&digests[0][0]))
	}
	err := withErrorBuffer(func(buffer unsafe.Pointer) C.vipr_status {
		return C.vipr_hash_file_tree(s.native, boolInt(usePatch), C.uint64_t(size), C.uint64_t(chunkSize), pointer, C.uint32_t(len(digests)), (*C.uint8_t)(unsafe.Pointer(&root[0])), cancel.pointer(), (*C.char)(buffer), errorBufferSize)
	})
	runtime.KeepAlive(s)
	runtime.KeepAlive(digests)
	return root, digests, err
}

func (s *Session) HashFile(ctx context.Context, usePatch bool, size uint64) (patchformat.Digest, error) {
	var result patchformat.Digest
	cancel, stop := newCancelWord(ctx)
	defer stop()
	err := withErrorBuffer(func(buffer unsafe.Pointer) C.vipr_status {
		return C.vipr_hash_file_standard(s.native, boolInt(usePatch), C.uint64_t(size), (*C.uint8_t)(unsafe.Pointer(&result[0])), cancel.pointer(), (*C.char)(buffer), errorBufferSize)
	})
	runtime.KeepAlive(s)
	return result, err
}

type cancelWord struct {
	value uint32
	done  chan struct{}
}

func newCancelWord(ctx context.Context) (*cancelWord, func()) {
	word := &cancelWord{done: make(chan struct{})}
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		select {
		case <-ctx.Done():
			atomic.StoreUint32(&word.value, 1)
		case <-word.done:
		}
	}()
	return word, func() { close(word.done) }
}
func (w *cancelWord) pointer() *C.uint32_t { return (*C.uint32_t)(unsafe.Pointer(&w.value)) }

type BuiltWindow struct {
	Descriptor patchformat.WindowDescriptor
	Payload    []byte
}

func (s *Session) BuildWindow(ctx context.Context, sourceSize, targetSize, offset uint64, outputSize, windowSize uint32, level int, mode patchformat.OptimizationMode) (BuiltWindow, error) {
	var result C.vipr_window_result
	cancel, stop := newCancelWord(ctx)
	defer stop()
	err := withErrorBuffer(func(buffer unsafe.Pointer) C.vipr_status {
		return C.vipr_build_window(s.native, C.uint64_t(sourceSize), C.uint64_t(targetSize), C.uint64_t(offset), C.uint32_t(outputSize), C.uint32_t(windowSize), C.int(level), C.uint8_t(mode), cancel.pointer(), &result, (*C.char)(buffer), errorBufferSize)
	})
	if err != nil {
		C.vipr_window_result_free(&result)
		return BuiltWindow{}, err
	}
	payload := C.GoBytes(unsafe.Pointer(result.payload), C.int(result.payload_size))
	descriptor := patchformat.WindowDescriptor{OutputOffset: offset, OutputSize: outputSize, Kind: patchformat.WindowKind(result.kind), Codec: patchformat.Codec(result.codec), Flags: uint16(result.flags), PayloadSize: uint32(result.payload_size), ExpandedSize: uint32(result.expanded_size), SourceOffset: uint64(result.source_offset), SourceSize: uint32(result.source_size), SourceFirstChunk: uint32(result.source_first_chunk), SourceChunkCount: uint16(result.source_chunk_count), InstructionCount: uint16(result.instruction_count)}
	copy(descriptor.Digest[:], C.GoBytes(unsafe.Pointer(&result.digest[0]), 32))
	C.vipr_window_result_free(&result)
	runtime.KeepAlive(s)
	return BuiltWindow{Descriptor: descriptor, Payload: payload}, nil
}

type GroupResult struct {
	BytesReadPatch, BytesReadSource, BytesWritten uint64
	WindowsCompleted                              uint32
}

type SourceVerification struct {
	Digests []patchformat.Digest
	States  []uint32
}

func NewSourceVerification(digests []patchformat.Digest, preverified bool) *SourceVerification {
	states := make([]uint32, len(digests))
	if preverified {
		for i := range states {
			states[i] = 2
		}
	}
	return &SourceVerification{Digests: digests, States: states}
}

func verificationPointers(verification *SourceVerification) (*C.uint8_t, C.uint32_t, *C.uint32_t) {
	if verification == nil || len(verification.Digests) == 0 {
		return nil, 0, nil
	}
	return (*C.uint8_t)(unsafe.Pointer(&verification.Digests[0][0])), C.uint32_t(len(verification.Digests)), (*C.uint32_t)(unsafe.Pointer(&verification.States[0]))
}

func resultFromC(value C.vipr_group_result) GroupResult {
	return GroupResult{uint64(value.bytes_read_patch), uint64(value.bytes_read_source), uint64(value.bytes_written), uint32(value.windows_completed)}
}

func (s *Session) ApplyGroup(ctx context.Context, windows []patchformat.WindowDescriptor, groupOffset uint64, groupSize uint32, sourceSize uint64, verification *SourceVerification, expected patchformat.Digest) (GroupResult, error) {
	if len(windows) == 0 {
		return GroupResult{}, fmt.Errorf("empty window group")
	}
	encodedWindows := patchformat.MarshalWindowDescriptors(windows)
	digests, count, states := verificationPointers(verification)
	cancel, stop := newCancelWord(ctx)
	defer stop()
	var result C.vipr_group_result
	err := withErrorBuffer(func(buffer unsafe.Pointer) C.vipr_status {
		return C.vipr_apply_group(s.native, (*C.uint8_t)(unsafe.Pointer(&encodedWindows[0])), C.uint32_t(len(windows)), C.uint64_t(groupOffset), C.uint32_t(groupSize), C.uint64_t(sourceSize), digests, count, states, (*C.uint8_t)(unsafe.Pointer(&expected[0])), cancel.pointer(), &result, (*C.char)(buffer), errorBufferSize)
	})
	runtime.KeepAlive(s)
	runtime.KeepAlive(encodedWindows)
	runtime.KeepAlive(verification)
	return resultFromC(result), err
}

func (s *Session) ApplyChangedWindow(ctx context.Context, window patchformat.WindowDescriptor, sourceSize uint64, verification *SourceVerification) (GroupResult, error) {
	encodedWindow := patchformat.MarshalWindowDescriptor(window)
	digests, count, states := verificationPointers(verification)
	cancel, stop := newCancelWord(ctx)
	defer stop()
	var result C.vipr_group_result
	err := withErrorBuffer(func(buffer unsafe.Pointer) C.vipr_status {
		return C.vipr_apply_changed_window(s.native, (*C.uint8_t)(unsafe.Pointer(&encodedWindow[0])), C.uint64_t(sourceSize), digests, count, states, cancel.pointer(), &result, (*C.char)(buffer), errorBufferSize)
	})
	runtime.KeepAlive(s)
	runtime.KeepAlive(encodedWindow)
	runtime.KeepAlive(verification)
	return resultFromC(result), err
}
func (s *Session) SetOutputSize(size uint64) error {
	return withErrorBuffer(func(buffer unsafe.Pointer) C.vipr_status {
		return C.vipr_set_file_size(s.native, C.uint64_t(size), (*C.char)(buffer), errorBufferSize)
	})
}
func (s *Session) FlushOutput() error {
	return withErrorBuffer(func(buffer unsafe.Pointer) C.vipr_status {
		return C.vipr_flush_output(s.native, (*C.char)(buffer), errorBufferSize)
	})
}

func (s *Session) CloneOutput(size uint64) error {
	if s == nil || s.native == nil {
		return fmt.Errorf("native session is unavailable")
	}
	err := withErrorBuffer(func(buffer unsafe.Pointer) C.vipr_status {
		return C.vipr_clone_output(s.native, C.uint64_t(size), (*C.char)(buffer), errorBufferSize)
	})
	runtime.KeepAlive(s)
	return err
}
