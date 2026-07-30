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
	"io"
	"os"
	"runtime"
	"sync"
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

func withErrorBuffer(call func(*C.char) C.vipr_status) error {
	var buffer [errorBufferSize]byte
	pointer := (*C.char)(unsafe.Pointer(&buffer[0]))
	return checkStatus(call(pointer), unsafe.Pointer(pointer))
}

type Session struct {
	native            *C.vipr_io_session
	source            *os.File
	patch             *os.File
	output            *os.File
	windowDescriptors []byte
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
	var buffer [errorBufferSize]byte
	pointer := (*C.char)(unsafe.Pointer(&buffer[0]))
	native := C.vipr_session_create(raw(source), raw(patch), raw(output), boolInt(source != nil), boolInt(patch != nil), boolInt(output != nil), C.int(profile), pointer, errorBufferSize)
	runtime.KeepAlive(source)
	runtime.KeepAlive(patch)
	runtime.KeepAlive(output)
	if native == nil {
		return nil, &NativeError{Status: StatusInternal, Detail: C.GoString(pointer)}
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

func ZstdVersion() string   { return C.GoString(C.vipr_zstd_version()) }
func BLAKE3Backend() string { return C.GoString(C.vipr_blake3_backend()) }

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
	token := NewCancelToken(ctx)
	defer token.Close()
	return s.HashFileTreeWithToken(token, usePatch, size, chunkSize)
}

func (s *Session) HashFileTreeWithToken(token *CancelToken, usePatch bool, size, chunkSize uint64) (patchformat.Digest, []patchformat.Digest, error) {
	if s == nil || s.native == nil {
		return patchformat.Digest{}, nil, fmt.Errorf("native session is unavailable")
	}
	if chunkSize == 0 {
		return patchformat.Digest{}, nil, fmt.Errorf("chunk size must be positive")
	}
	count := 0
	if size != 0 {
		count = int((size + chunkSize - 1) / chunkSize)
	}
	digests := make([]patchformat.Digest, count)
	var root patchformat.Digest
	var pointer *C.uint8_t
	if len(digests) != 0 {
		pointer = (*C.uint8_t)(unsafe.Pointer(&digests[0][0]))
	}
	err := withErrorBuffer(func(buffer *C.char) C.vipr_status {
		return C.vipr_hash_file_tree(s.native, boolInt(usePatch), C.uint64_t(size), C.uint64_t(chunkSize), pointer, C.uint32_t(len(digests)), (*C.uint8_t)(unsafe.Pointer(&root[0])), token.pointer(), buffer, errorBufferSize)
	})
	runtime.KeepAlive(s)
	runtime.KeepAlive(digests)
	runtime.KeepAlive(token)
	return root, digests, err
}

func (s *Session) HashFile(ctx context.Context, usePatch bool, size uint64) (patchformat.Digest, error) {
	token := NewCancelToken(ctx)
	defer token.Close()
	return s.HashFileWithToken(token, usePatch, size)
}

func (s *Session) HashFileWithToken(token *CancelToken, usePatch bool, size uint64) (patchformat.Digest, error) {
	if s == nil || s.native == nil {
		return patchformat.Digest{}, fmt.Errorf("native session is unavailable")
	}
	var result patchformat.Digest
	err := withErrorBuffer(func(buffer *C.char) C.vipr_status {
		return C.vipr_hash_file_standard(s.native, boolInt(usePatch), C.uint64_t(size), (*C.uint8_t)(unsafe.Pointer(&result[0])), token.pointer(), buffer, errorBufferSize)
	})
	runtime.KeepAlive(s)
	runtime.KeepAlive(token)
	return result, err
}

type CancelToken struct {
	value uint32
	stop  func() bool
	once  sync.Once
}

func NewCancelToken(ctx context.Context) *CancelToken {
	if ctx == nil {
		ctx = context.Background()
	}
	token := &CancelToken{}
	if ctx.Err() != nil {
		atomic.StoreUint32(&token.value, 1)
	}
	token.stop = context.AfterFunc(ctx, func() {
		atomic.StoreUint32(&token.value, 1)
	})
	return token
}

func (t *CancelToken) Close() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		if t.stop != nil {
			t.stop()
		}
	})
}

func (t *CancelToken) pointer() *C.uint32_t {
	if t == nil {
		return nil
	}
	return (*C.uint32_t)(unsafe.Pointer(&t.value))
}

type BuiltWindow struct {
	Descriptor patchformat.WindowDescriptor
	Payload    []byte
}

type BorrowedWindow struct {
	session    *Session
	result     C.vipr_window_result
	Descriptor patchformat.WindowDescriptor
	released   bool
}

func descriptorFromResult(offset uint64, outputSize uint32, result *C.vipr_window_result) patchformat.WindowDescriptor {
	descriptor := patchformat.WindowDescriptor{
		OutputOffset:     offset,
		OutputSize:       outputSize,
		Kind:             patchformat.WindowKind(result.kind),
		Codec:            patchformat.Codec(result.codec),
		Flags:            uint16(result.flags),
		PayloadSize:      uint32(result.payload_size),
		ExpandedSize:     uint32(result.expanded_size),
		SourceOffset:     uint64(result.source_offset),
		SourceSize:       uint32(result.source_size),
		SourceFirstChunk: uint32(result.source_first_chunk),
		SourceChunkCount: uint16(result.source_chunk_count),
		InstructionCount: uint16(result.instruction_count),
	}
	copy(descriptor.Digest[:], unsafe.Slice((*byte)(unsafe.Pointer(&result.digest[0])), len(descriptor.Digest)))
	return descriptor
}

func (s *Session) BuildWindow(ctx context.Context, sourceSize, targetSize, offset uint64, outputSize, windowSize uint32, level int, mode patchformat.OptimizationMode) (BuiltWindow, error) {
	token := NewCancelToken(ctx)
	defer token.Close()
	borrowed, err := s.BuildWindowBorrowed(token, sourceSize, targetSize, offset, outputSize, windowSize, level, mode)
	if err != nil {
		return BuiltWindow{}, err
	}
	defer borrowed.Release()
	payload := C.GoBytes(unsafe.Pointer(borrowed.result.payload), C.int(borrowed.result.payload_size))
	return BuiltWindow{Descriptor: borrowed.Descriptor, Payload: payload}, nil
}

func (s *Session) BuildWindowBorrowed(token *CancelToken, sourceSize, targetSize, offset uint64, outputSize, windowSize uint32, level int, mode patchformat.OptimizationMode) (*BorrowedWindow, error) {
	if s == nil || s.native == nil {
		return nil, fmt.Errorf("native session is unavailable")
	}
	borrowed := &BorrowedWindow{session: s}
	err := withErrorBuffer(func(buffer *C.char) C.vipr_status {
		return C.vipr_build_window(s.native, C.uint64_t(sourceSize), C.uint64_t(targetSize), C.uint64_t(offset), C.uint32_t(outputSize), C.uint32_t(windowSize), C.int(level), C.uint8_t(mode), token.pointer(), &borrowed.result, buffer, errorBufferSize)
	})
	if err != nil {
		return nil, err
	}
	borrowed.Descriptor = descriptorFromResult(offset, outputSize, &borrowed.result)
	runtime.KeepAlive(s)
	runtime.KeepAlive(token)
	return borrowed, nil
}

func (w *BorrowedWindow) WritePayloadAt(offset uint64) error {
	if w == nil || w.session == nil || w.released {
		return fmt.Errorf("borrowed V4 window is unavailable")
	}
	err := withErrorBuffer(func(buffer *C.char) C.vipr_status {
		return C.vipr_write_window_payload(w.session.native, &w.result, C.uint64_t(offset), buffer, errorBufferSize)
	})
	runtime.KeepAlive(w.session)
	return err
}

func (w *BorrowedWindow) Release() {
	if w == nil || w.released {
		return
	}
	C.vipr_window_result_release(w.session.native, &w.result)
	w.released = true
	w.session = nil
}

type GroupResult struct {
	BytesReadPatch, BytesReadSource, BytesWritten uint64
	WindowsCompleted                              uint32
}

type SourceVerification struct {
	Digests   []patchformat.Digest
	States    []uint32
	source    unsafe.Pointer
	sourceSize uint64
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

// LoadSource reads one stable source image into C-owned memory. When verify is
// true each canonical chunk is authenticated before the cache becomes visible
// to native COPY decoding.
func (verification *SourceVerification) LoadSource(ctx context.Context, source *os.File, size uint64, verify bool) error {
	if verification == nil || source == nil {
		return fmt.Errorf("source cache requires a verification object and file")
	}
	if verification.source != nil {
		return fmt.Errorf("source cache is already loaded")
	}
	if size == 0 {
		return nil
	}
	if size > uint64(^uint(0)>>1) {
		return fmt.Errorf("source cache is too large for this architecture")
	}
	pointer := C.malloc(C.size_t(size))
	if pointer == nil {
		return &NativeError{Status: StatusMemoryLimit, Detail: "allocate source cache"}
	}
	data := unsafe.Slice((*byte)(pointer), int(size))
	for offset := uint64(0); offset < size; {
		if err := ctx.Err(); err != nil {
			C.free(pointer)
			return err
		}
		count := min(uint64(patchformat.IdentityChunkSize), size-offset)
		read, err := source.ReadAt(data[int(offset):int(offset+count)], int64(offset))
		if err != nil && !(errors.Is(err, io.EOF) && read == int(count)) {
			C.free(pointer)
			return fmt.Errorf("read source cache at %d: %w", offset, err)
		}
		if read != int(count) {
			C.free(pointer)
			return io.ErrUnexpectedEOF
		}
		if verify {
			index := offset / patchformat.IdentityChunkSize
			if index >= uint64(len(verification.Digests)) {
				C.free(pointer)
				return fmt.Errorf("source cache exceeds digest table")
			}
			actual, err := HashBytes(data[int(offset):int(offset+count)])
			if err != nil {
				C.free(pointer)
				return err
			}
			if actual != verification.Digests[index] {
				C.free(pointer)
				verification.States[index] = 3
				return fmt.Errorf("source chunk %d digest mismatch", index)
			}
			verification.States[index] = 2
		}
		offset += count
	}
	verification.source = pointer
	verification.sourceSize = size
	return nil
}

func (verification *SourceVerification) Close() {
	if verification == nil || verification.source == nil {
		return
	}
	C.free(verification.source)
	verification.source = nil
	verification.sourceSize = 0
}

func verificationPointers(verification *SourceVerification) (*C.uint8_t, C.uint32_t, *C.uint32_t, *C.uint8_t, C.uint64_t) {
	if verification == nil {
		return nil, 0, nil, nil, 0
	}
	var digests *C.uint8_t
	var states *C.uint32_t
	if len(verification.Digests) != 0 {
		digests = (*C.uint8_t)(unsafe.Pointer(&verification.Digests[0][0]))
		states = (*C.uint32_t)(unsafe.Pointer(&verification.States[0]))
	}
	return digests, C.uint32_t(len(verification.Digests)), states,
		(*C.uint8_t)(verification.source), C.uint64_t(verification.sourceSize)
}

func resultFromC(value C.vipr_group_result) GroupResult {
	return GroupResult{uint64(value.bytes_read_patch), uint64(value.bytes_read_source), uint64(value.bytes_written), uint32(value.windows_completed)}
}

func (s *Session) ApplyGroup(ctx context.Context, windows []patchformat.WindowDescriptor, groupOffset uint64, groupSize uint32, sourceSize uint64, verification *SourceVerification, expected patchformat.Digest) (GroupResult, error) {
	token := NewCancelToken(ctx)
	defer token.Close()
	return s.ApplyGroupWithToken(token, windows, groupOffset, groupSize, sourceSize, verification, expected)
}

func (s *Session) ApplyGroupWithToken(token *CancelToken, windows []patchformat.WindowDescriptor, groupOffset uint64, groupSize uint32, sourceSize uint64, verification *SourceVerification, expected patchformat.Digest) (GroupResult, error) {
	if s == nil || s.native == nil {
		return GroupResult{}, fmt.Errorf("native session is unavailable")
	}
	if len(windows) == 0 {
		return GroupResult{}, fmt.Errorf("empty window group")
	}
	required := len(windows) * patchformat.WindowDescriptorSize
	if cap(s.windowDescriptors) < required {
		s.windowDescriptors = make([]byte, required)
	} else {
		s.windowDescriptors = s.windowDescriptors[:required]
	}
	for index, window := range windows {
		encoded := patchformat.MarshalWindowDescriptor(window)
		start := index * patchformat.WindowDescriptorSize
		copy(s.windowDescriptors[start:start+patchformat.WindowDescriptorSize], encoded[:])
	}
	encodedWindows := s.windowDescriptors
	digests, count, states, sourceCache, sourceCacheSize := verificationPointers(verification)
	var result C.vipr_group_result
	err := withErrorBuffer(func(buffer *C.char) C.vipr_status {
		return C.vipr_apply_group(s.native, (*C.uint8_t)(unsafe.Pointer(&encodedWindows[0])), C.uint32_t(len(windows)), C.uint64_t(groupOffset), C.uint32_t(groupSize), C.uint64_t(sourceSize), digests, count, states, sourceCache, sourceCacheSize, (*C.uint8_t)(unsafe.Pointer(&expected[0])), token.pointer(), &result, buffer, errorBufferSize)
	})
	runtime.KeepAlive(s)
	runtime.KeepAlive(encodedWindows)
	runtime.KeepAlive(verification)
	runtime.KeepAlive(token)
	return resultFromC(result), err
}

func (s *Session) ApplyChangedWindow(ctx context.Context, window patchformat.WindowDescriptor, sourceSize uint64, verification *SourceVerification) (GroupResult, error) {
	token := NewCancelToken(ctx)
	defer token.Close()
	return s.ApplyChangedWindowWithToken(token, window, sourceSize, verification)
}

func (s *Session) ApplyChangedWindowWithToken(token *CancelToken, window patchformat.WindowDescriptor, sourceSize uint64, verification *SourceVerification) (GroupResult, error) {
	if s == nil || s.native == nil {
		return GroupResult{}, fmt.Errorf("native session is unavailable")
	}
	encodedWindow := patchformat.MarshalWindowDescriptor(window)
	digests, count, states, sourceCache, sourceCacheSize := verificationPointers(verification)
	var result C.vipr_group_result
	err := withErrorBuffer(func(buffer *C.char) C.vipr_status {
		return C.vipr_apply_changed_window(s.native, (*C.uint8_t)(unsafe.Pointer(&encodedWindow[0])), C.uint64_t(sourceSize), digests, count, states, sourceCache, sourceCacheSize, token.pointer(), &result, buffer, errorBufferSize)
	})
	runtime.KeepAlive(s)
	runtime.KeepAlive(encodedWindow)
	runtime.KeepAlive(verification)
	runtime.KeepAlive(token)
	return resultFromC(result), err
}

func (s *Session) SetOutputSize(size uint64, preallocate ...bool) error {
	if s == nil || s.native == nil {
		return fmt.Errorf("native session is unavailable")
	}
	return withErrorBuffer(func(buffer *C.char) C.vipr_status {
		enabled := len(preallocate) != 0 && preallocate[0]
		return C.vipr_set_file_size(s.native, C.uint64_t(size), boolInt(enabled), buffer, errorBufferSize)
	})
}

func (s *Session) FlushOutput() error {
	if s == nil || s.native == nil {
		return fmt.Errorf("native session is unavailable")
	}
	return withErrorBuffer(func(buffer *C.char) C.vipr_status {
		return C.vipr_flush_output(s.native, buffer, errorBufferSize)
	})
}

func (s *Session) CloneOutput(size uint64) error {
	if s == nil || s.native == nil {
		return fmt.Errorf("native session is unavailable")
	}
	err := withErrorBuffer(func(buffer *C.char) C.vipr_status {
		return C.vipr_clone_output(s.native, C.uint64_t(size), buffer, errorBufferSize)
	})
	runtime.KeepAlive(s)
	return err
}

type SessionPool struct {
	available chan *Session
	all       []*Session
	closed    chan struct{}
	once      sync.Once
}

func NewSessionPool(count int, source, patch, output *os.File, profile IOProfile) (*SessionPool, error) {
	if count < 1 {
		return nil, fmt.Errorf("native session pool size must be positive")
	}
	pool := &SessionPool{
		available: make(chan *Session, count),
		all:       make([]*Session, 0, count),
		closed:    make(chan struct{}),
	}
	for range count {
		session, err := NewSessionWithProfile(source, patch, output, profile)
		if err != nil {
			pool.Close()
			return nil, err
		}
		pool.all = append(pool.all, session)
		pool.available <- session
	}
	return pool, nil
}

func (p *SessionPool) Acquire(ctx context.Context) (*Session, error) {
	if p == nil || p.available == nil || p.closed == nil {
		return nil, fmt.Errorf("native session pool is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-p.closed:
		return nil, fmt.Errorf("native session pool is closed")
	default:
	}
	select {
	case session := <-p.available:
		select {
		case <-p.closed:
			p.available <- session
			return nil, fmt.Errorf("native session pool is closed")
		default:
			return session, nil
		}
	case <-p.closed:
		return nil, fmt.Errorf("native session pool is closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *SessionPool) Release(session *Session) {
	if p == nil || session == nil || p.available == nil {
		return
	}
	p.available <- session
}

func (p *SessionPool) Close() error {
	if p == nil {
		return nil
	}
	var first error
	p.once.Do(func() {
		close(p.closed)
		for range len(p.all) {
			session := <-p.available
			if err := session.Close(); err != nil && first == nil {
				first = err
			}
		}
		p.all = nil
	})
	return first
}
