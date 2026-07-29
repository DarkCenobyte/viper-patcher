#ifndef _WIN32
#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif
#endif
#include "native_internal.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#include <winioctl.h>
#endif

#ifndef _WIN32
#include <fcntl.h>
#include <sys/ioctl.h>
#include <sys/stat.h>
#include <unistd.h>
#ifndef O_CLOEXEC
#define O_CLOEXEC 0
#endif
#if defined(__linux__)
#include <linux/fs.h>
#endif
#if defined(__APPLE__)
#include <copyfile.h>
#endif
#endif

void vipr_set_error(char *buffer, size_t size, const char *message) {
    if (buffer == NULL || size == 0) return;
    if (message == NULL) message = "unknown error";
    snprintf(buffer, size, "%s", message);
    buffer[size - 1] = '\0';
}

void vipr_set_system_error(char *buffer, size_t size, const char *operation) {
#ifdef _WIN32
    DWORD code = GetLastError();
    char system_message[512] = {0};
    FormatMessageA(FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS, NULL, code, 0,
                   system_message, (DWORD)sizeof(system_message), NULL);
    if (buffer != NULL && size > 0) {
        snprintf(buffer, size, "%s: Windows error %lu: %s", operation, (unsigned long)code, system_message);
        buffer[size - 1] = '\0';
    }
#else
    if (buffer != NULL && size > 0) {
        snprintf(buffer, size, "%s: %s", operation, strerror(errno));
        buffer[size - 1] = '\0';
    }
#endif
}

void vipr_set_zstd_error(char *buffer, size_t size, const char *operation, size_t code) {
    if (buffer != NULL && size > 0) {
        snprintf(buffer, size, "%s: %s", operation, ZSTD_getErrorName(code));
        buffer[size - 1] = '\0';
    }
}

int vipr_cancelled(const volatile uint32_t *cancel) {
    if (cancel == NULL) return 0;
#if defined(__GNUC__) || defined(__clang__)
    return __atomic_load_n(cancel, __ATOMIC_ACQUIRE) != 0;
#else
    return *cancel != 0;
#endif
}

const char *vipr_status_name(vipr_status status) {
    switch (status) {
        case VIPR_STATUS_OK: return "ok";
        case VIPR_STATUS_CANCELLED: return "cancelled";
        case VIPR_STATUS_INVALID_ARGUMENT: return "invalid argument";
        case VIPR_STATUS_INVALID_WINDOW: return "invalid window";
        case VIPR_STATUS_SOURCE_MISMATCH: return "source mismatch";
        case VIPR_STATUS_OUTPUT_MISMATCH: return "output mismatch";
        case VIPR_STATUS_READ_ERROR: return "read error";
        case VIPR_STATUS_WRITE_ERROR: return "write error";
        case VIPR_STATUS_ZSTD_ERROR: return "zstd error";
        case VIPR_STATUS_MEMORY_LIMIT: return "memory limit";
        case VIPR_STATUS_UNSUPPORTED: return "unsupported";
        default: return "internal error";
    }
}

const char *vipr_zstd_version(void) { return ZSTD_versionString(); }

#ifdef _WIN32
static HANDLE reopen_handle(uintptr_t raw, DWORD access, DWORD flags, int *owned,
                            char *error_buffer, size_t error_buffer_size) {
    if (raw == 0 || raw == (uintptr_t)INVALID_HANDLE_VALUE) {
        vipr_set_error(error_buffer, error_buffer_size, "invalid Windows file handle");
        return INVALID_HANDLE_VALUE;
    }
    HANDLE reopened = ReOpenFile((HANDLE)raw, access,
                                 FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                                 flags | FILE_FLAG_OVERLAPPED);
    if (reopened == INVALID_HANDLE_VALUE) {
        vipr_set_system_error(error_buffer, error_buffer_size, "ReOpenFile");
        return INVALID_HANDLE_VALUE;
    }
    *owned = 1;
    return reopened;
}
#else
static int reopen_handle(uintptr_t raw, int access, int flags, int *owned,
                         char *error_buffer, size_t error_buffer_size) {
    (void)access; (void)flags; (void)error_buffer; (void)error_buffer_size;
    *owned = 0;
    return (int)raw;
}
#endif

vipr_io_session *vipr_session_create(uintptr_t source_handle, uintptr_t patch_handle, uintptr_t output_handle,
                                     int need_source, int need_patch, int need_output, int io_profile,
                                     char *error_buffer, size_t error_buffer_size) {
    vipr_io_session *session = (vipr_io_session *)calloc(1, sizeof(*session));
    if (session == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "allocate native I/O session");
        return NULL;
    }
#ifdef _WIN32
    session->source = INVALID_HANDLE_VALUE;
    session->patch = INVALID_HANDLE_VALUE;
    session->output = INVALID_HANDLE_VALUE;
    DWORD source_flags = io_profile == 1 ? FILE_FLAG_SEQUENTIAL_SCAN : FILE_FLAG_RANDOM_ACCESS;
    DWORD patch_flags = io_profile == 1 ? FILE_FLAG_SEQUENTIAL_SCAN : FILE_FLAG_RANDOM_ACCESS;
    if (need_source) {
        session->source = reopen_handle(source_handle, GENERIC_READ, source_flags,
                                        &session->owns_source, error_buffer, error_buffer_size);
        if (session->source == INVALID_HANDLE_VALUE) goto fail;
    }
    if (need_patch) {
        session->patch = reopen_handle(patch_handle, GENERIC_READ, patch_flags,
                                       &session->owns_patch, error_buffer, error_buffer_size);
        if (session->patch == INVALID_HANDLE_VALUE) goto fail;
    }
    if (need_output) {
        session->output = reopen_handle(output_handle, GENERIC_READ | GENERIC_WRITE, FILE_FLAG_RANDOM_ACCESS,
                                        &session->owns_output, error_buffer, error_buffer_size);
        if (session->output == INVALID_HANDLE_VALUE) goto fail;
    }
#else
    session->source = need_source ? reopen_handle(source_handle, 0, 0, &session->owns_source, error_buffer, error_buffer_size) : -1;
    session->patch = need_patch ? reopen_handle(patch_handle, 0, 0, &session->owns_patch, error_buffer, error_buffer_size) : -1;
    session->output = need_output ? reopen_handle(output_handle, 0, 0, &session->owns_output, error_buffer, error_buffer_size) : -1;
#if defined(POSIX_FADV_RANDOM) && defined(POSIX_FADV_SEQUENTIAL)
    if (session->source >= 0) {
        (void)posix_fadvise(session->source, 0, 0, io_profile == 1 ? POSIX_FADV_SEQUENTIAL : POSIX_FADV_RANDOM);
    }
    if (session->patch >= 0) {
        (void)posix_fadvise(session->patch, 0, 0, io_profile == 1 ? POSIX_FADV_SEQUENTIAL : POSIX_FADV_RANDOM);
    }
#endif
#endif
    return session;
#ifdef _WIN32
fail:
    vipr_session_free(session);
    return NULL;
#endif
}

void vipr_session_free(vipr_io_session *session) {
    if (session == NULL) return;
#ifdef _WIN32
    if (session->owns_source && session->source != INVALID_HANDLE_VALUE) CloseHandle(session->source);
    if (session->owns_patch && session->patch != INVALID_HANDLE_VALUE) CloseHandle(session->patch);
    if (session->owns_output && session->output != INVALID_HANDLE_VALUE) CloseHandle(session->output);
#else
    if (session->owns_source && session->source >= 0) close(session->source);
    if (session->owns_patch && session->patch >= 0) close(session->patch);
    if (session->owns_output && session->output >= 0) close(session->output);
#endif
    free(session);
}

vipr_status vipr_read_at(vipr_handle handle, uint64_t offset, void *buffer, size_t size,
                         char *error_buffer, size_t error_buffer_size) {
    uint8_t *cursor = (uint8_t *)buffer;
    size_t remaining = size;
#ifdef _WIN32
    while (remaining > 0) {
        DWORD request = remaining > 0x7ffff000u ? 0x7ffff000u : (DWORD)remaining;
        OVERLAPPED overlapped;
        memset(&overlapped, 0, sizeof(overlapped));
        overlapped.Offset = (DWORD)offset;
        overlapped.OffsetHigh = (DWORD)(offset >> 32);
        HANDLE event = CreateEventW(NULL, TRUE, FALSE, NULL);
        if (event == NULL) { vipr_set_system_error(error_buffer, error_buffer_size, "CreateEvent"); return VIPR_STATUS_READ_ERROR; }
        overlapped.hEvent = event;
        DWORD read_size = 0;
        BOOL ok = ReadFile(handle, cursor, request, &read_size, &overlapped);
        if (!ok && GetLastError() == ERROR_IO_PENDING) ok = GetOverlappedResult(handle, &overlapped, &read_size, TRUE);
        CloseHandle(event);
        if (!ok || read_size != request) { vipr_set_system_error(error_buffer, error_buffer_size, "ReadFile"); return VIPR_STATUS_READ_ERROR; }
        cursor += read_size; remaining -= read_size; offset += read_size;
    }
#else
    while (remaining > 0) {
        ssize_t count = pread(handle, cursor, remaining, (off_t)offset);
        if (count < 0) { if (errno == EINTR) continue; vipr_set_system_error(error_buffer, error_buffer_size, "pread"); return VIPR_STATUS_READ_ERROR; }
        if (count == 0) { vipr_set_error(error_buffer, error_buffer_size, "unexpected end of file"); return VIPR_STATUS_READ_ERROR; }
        cursor += (size_t)count; remaining -= (size_t)count; offset += (uint64_t)count;
    }
#endif
    return VIPR_STATUS_OK;
}

vipr_status vipr_write_at(vipr_handle handle, uint64_t offset, const void *buffer, size_t size,
                          char *error_buffer, size_t error_buffer_size) {
    const uint8_t *cursor = (const uint8_t *)buffer;
    size_t remaining = size;
#ifdef _WIN32
    while (remaining > 0) {
        DWORD request = remaining > 0x7ffff000u ? 0x7ffff000u : (DWORD)remaining;
        OVERLAPPED overlapped;
        memset(&overlapped, 0, sizeof(overlapped));
        overlapped.Offset = (DWORD)offset;
        overlapped.OffsetHigh = (DWORD)(offset >> 32);
        HANDLE event = CreateEventW(NULL, TRUE, FALSE, NULL);
        if (event == NULL) { vipr_set_system_error(error_buffer, error_buffer_size, "CreateEvent"); return VIPR_STATUS_WRITE_ERROR; }
        overlapped.hEvent = event;
        DWORD written = 0;
        BOOL ok = WriteFile(handle, cursor, request, &written, &overlapped);
        if (!ok && GetLastError() == ERROR_IO_PENDING) ok = GetOverlappedResult(handle, &overlapped, &written, TRUE);
        CloseHandle(event);
        if (!ok || written != request) { vipr_set_system_error(error_buffer, error_buffer_size, "WriteFile"); return VIPR_STATUS_WRITE_ERROR; }
        cursor += written; remaining -= written; offset += written;
    }
#else
    while (remaining > 0) {
        ssize_t count = pwrite(handle, cursor, remaining, (off_t)offset);
        if (count < 0) { if (errno == EINTR) continue; vipr_set_system_error(error_buffer, error_buffer_size, "pwrite"); return VIPR_STATUS_WRITE_ERROR; }
        if (count == 0) { vipr_set_error(error_buffer, error_buffer_size, "short write"); return VIPR_STATUS_WRITE_ERROR; }
        cursor += (size_t)count; remaining -= (size_t)count; offset += (uint64_t)count;
    }
#endif
    return VIPR_STATUS_OK;
}

vipr_status vipr_resize(vipr_handle handle, uint64_t size, char *error_buffer, size_t error_buffer_size) {
#ifdef _WIN32
    FILE_END_OF_FILE_INFO info;
    info.EndOfFile.QuadPart = (LONGLONG)size;
    if (!SetFileInformationByHandle(handle, FileEndOfFileInfo, &info, sizeof(info))) {
        vipr_set_system_error(error_buffer, error_buffer_size, "SetFileInformationByHandle");
        return VIPR_STATUS_WRITE_ERROR;
    }
#else
    if (ftruncate(handle, (off_t)size) != 0) { vipr_set_system_error(error_buffer, error_buffer_size, "ftruncate"); return VIPR_STATUS_WRITE_ERROR; }
#if defined(__linux__)
    if (size > 0) (void)posix_fallocate(handle, 0, (off_t)size);
#endif
#endif
    return VIPR_STATUS_OK;
}

vipr_status vipr_flush(vipr_handle handle, char *error_buffer, size_t error_buffer_size) {
#ifdef _WIN32
    if (!FlushFileBuffers(handle)) { vipr_set_system_error(error_buffer, error_buffer_size, "FlushFileBuffers"); return VIPR_STATUS_WRITE_ERROR; }
#else
    if (fsync(handle) != 0) { vipr_set_system_error(error_buffer, error_buffer_size, "fsync"); return VIPR_STATUS_WRITE_ERROR; }
#endif
    return VIPR_STATUS_OK;
}

vipr_status vipr_set_file_size(vipr_io_session *session, uint64_t size, char *error_buffer, size_t error_buffer_size) {
    if (session == NULL) return VIPR_STATUS_INVALID_ARGUMENT;
    return vipr_resize(session->output, size, error_buffer, error_buffer_size);
}

vipr_status vipr_flush_output(vipr_io_session *session, char *error_buffer, size_t error_buffer_size) {
    if (session == NULL) return VIPR_STATUS_INVALID_ARGUMENT;
    return vipr_flush(session->output, error_buffer, error_buffer_size);
}

uint64_t vipr_hash64(const uint8_t *data, size_t size) {
    uint64_t hash = 1469598103934665603ULL;
    for (size_t i = 0; i < size; ++i) { hash ^= data[i]; hash *= 1099511628211ULL; }
    hash ^= hash >> 33; hash *= 0xff51afd7ed558ccdULL; hash ^= hash >> 33;
    return hash;
}

size_t vipr_write_uvarint(uint8_t *output, uint64_t value) {
    size_t count = 0;
    while (value >= 0x80) { output[count++] = (uint8_t)value | 0x80u; value >>= 7; }
    output[count++] = (uint8_t)value;
    return count;
}

int vipr_read_uvarint(const uint8_t *input, size_t input_size, size_t *cursor, uint64_t *value) {
    uint64_t result = 0;
    unsigned shift = 0;
    for (unsigned i = 0; i < 10; ++i) {
        if (*cursor >= input_size) return 0;
        uint8_t byte = input[(*cursor)++];
        if (i == 9 && byte > 1) return 0;
        result |= (uint64_t)(byte & 0x7fu) << shift;
        if ((byte & 0x80u) == 0) { *value = result; return 1; }
        shift += 7;
    }
    return 0;
}

vipr_status vipr_clone_output(vipr_io_session *session, uint64_t size,
                              char *error_buffer, size_t error_buffer_size) {
    if (session == NULL) return VIPR_STATUS_INVALID_ARGUMENT;
#if defined(__APPLE__)
#ifndef COPYFILE_CLONE
    (void)size; (void)error_buffer; (void)error_buffer_size;
    return VIPR_STATUS_UNSUPPORTED;
#else
    if (ftruncate(session->output, 0) != 0) {
        vipr_set_system_error(error_buffer, error_buffer_size, "prepare clone output");
        return VIPR_STATUS_WRITE_ERROR;
    }
    if (fcopyfile(session->source, session->output, NULL, COPYFILE_DATA | COPYFILE_CLONE) == 0) {
        return VIPR_STATUS_OK;
    }
    if (errno == ENOTSUP || errno == EXDEV || errno == EINVAL) return VIPR_STATUS_UNSUPPORTED;
    vipr_set_system_error(error_buffer, error_buffer_size, "fcopyfile clone");
    return VIPR_STATUS_WRITE_ERROR;
#endif
#elif defined(__linux__)
    (void)size;
    if (ftruncate(session->output, 0) != 0) {
        vipr_set_system_error(error_buffer, error_buffer_size, "prepare clone output");
        return VIPR_STATUS_WRITE_ERROR;
    }
    if (ioctl(session->output, FICLONE, session->source) == 0) return VIPR_STATUS_OK;
    if (errno == EOPNOTSUPP || errno == ENOTTY || errno == EXDEV || errno == EINVAL) return VIPR_STATUS_UNSUPPORTED;
    vipr_set_system_error(error_buffer, error_buffer_size, "FICLONE");
    return VIPR_STATUS_WRITE_ERROR;
#elif defined(_WIN32)
    FILE_END_OF_FILE_INFO eof;
    eof.EndOfFile.QuadPart = (LONGLONG)size;
    if (!SetFileInformationByHandle(session->output, FileEndOfFileInfo, &eof, sizeof(eof))) {
        return VIPR_STATUS_UNSUPPORTED;
    }
    uint64_t offset = 0;
    while (offset < size) {
        uint64_t length = size - offset;
        if (length > (1ULL << 30)) length = 1ULL << 30;
        DUPLICATE_EXTENTS_DATA data;
        data.FileHandle = session->source;
        data.SourceFileOffset.QuadPart = (LONGLONG)offset;
        data.TargetFileOffset.QuadPart = (LONGLONG)offset;
        data.ByteCount.QuadPart = (LONGLONG)length;
        DWORD returned = 0;
        OVERLAPPED overlapped;
        memset(&overlapped, 0, sizeof(overlapped));
        HANDLE event = CreateEventW(NULL, TRUE, FALSE, NULL);
        if (event == NULL) return VIPR_STATUS_UNSUPPORTED;
        overlapped.hEvent = event;
        BOOL ok = DeviceIoControl(session->output, FSCTL_DUPLICATE_EXTENTS_TO_FILE,
                                  &data, sizeof(data), NULL, 0, &returned, &overlapped);
        if (!ok && GetLastError() == ERROR_IO_PENDING) {
            ok = GetOverlappedResult(session->output, &overlapped, &returned, TRUE);
        }
        CloseHandle(event);
        if (!ok) return VIPR_STATUS_UNSUPPORTED;
        offset += length;
    }
    return VIPR_STATUS_OK;
#else
    (void)size; (void)error_buffer; (void)error_buffer_size;
    return VIPR_STATUS_UNSUPPORTED;
#endif
}
