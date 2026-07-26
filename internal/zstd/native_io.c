#if !defined(_WIN32) && !defined(_POSIX_C_SOURCE)
#define _POSIX_C_SOURCE 200809L
#endif

#include "native_internal.h"

#include <errno.h>
#include <limits.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#if defined(_WIN32)
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <fcntl.h>
#include <io.h>
#include <wchar.h>
#else
#include <fcntl.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>
#endif

#if defined(_WIN32)
static wchar_t *vipr_utf8_to_wide(const char *value) {
    int length;
    wchar_t *wide;
    if (value == NULL) {
        return NULL;
    }
    length = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value, -1, NULL, 0);
    if (length <= 0) {
        errno = EINVAL;
        return NULL;
    }
    wide = (wchar_t *)malloc((size_t)length * sizeof(wchar_t));
    if (wide == NULL) {
        errno = ENOMEM;
        return NULL;
    }
    if (MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value, -1, wide, length) <= 0) {
        free(wide);
        errno = EINVAL;
        return NULL;
    }
    return wide;
}

static FILE *vipr_fopen_utf8(const char *path, const wchar_t *mode) {
    wchar_t *wide_path = vipr_utf8_to_wide(path);
    FILE *file;
    if (wide_path == NULL) {
        return NULL;
    }
    file = _wfopen(wide_path, mode);
    free(wide_path);
    return file;
}

static HANDLE vipr_duplicate_handle(uintptr_t handle_value) {
    HANDLE duplicate = NULL;
    if (!DuplicateHandle(
            GetCurrentProcess(),
            (HANDLE)handle_value,
            GetCurrentProcess(),
            &duplicate,
            0,
            FALSE,
            DUPLICATE_SAME_ACCESS)) {
        errno = EIO;
        return NULL;
    }
    return duplicate;
}

static FILE *vipr_open_handle(uintptr_t handle_value, int flags, const char *mode) {
    HANDLE duplicate = vipr_duplicate_handle(handle_value);
    int descriptor;
    FILE *file;
    if (duplicate == NULL) {
        return NULL;
    }
    descriptor = _open_osfhandle((intptr_t)duplicate, _O_BINARY | flags);
    if (descriptor < 0) {
        CloseHandle(duplicate);
        return NULL;
    }
    file = _fdopen(descriptor, mode);
    if (file == NULL) {
        _close(descriptor);
        return NULL;
    }
    return file;
}

FILE *vipr_open_read(const char *path) {
    return vipr_fopen_utf8(path, L"rb");
}

FILE *vipr_open_write(const char *path) {
    return vipr_fopen_utf8(path, L"wb");
}

FILE *vipr_open_write_handle(uintptr_t handle_value) {
    return vipr_open_handle(handle_value, _O_WRONLY, "wb");
}
#else
FILE *vipr_open_read(const char *path) {
    return fopen(path, "rb");
}

FILE *vipr_open_write(const char *path) {
    return fopen(path, "wb");
}

static FILE *vipr_open_handle(uintptr_t handle_value, const char *mode) {
    int descriptor = dup((int)handle_value);
    FILE *file;
    if (descriptor < 0) {
        return NULL;
    }
    file = fdopen(descriptor, mode);
    if (file == NULL) {
        close(descriptor);
        return NULL;
    }
    return file;
}

FILE *vipr_open_write_handle(uintptr_t handle_value) {
    return vipr_open_handle(handle_value, "wb");
}
#endif

int vipr_read_at(uintptr_t handle_value, uint64_t offset, void *buffer, size_t size, size_t *read_size) {
    if (read_size == NULL || (size > 0 && buffer == NULL)) {
        errno = EINVAL;
        return -1;
    }
    *read_size = 0;
#if defined(_WIN32)
    {
        OVERLAPPED overlapped;
        DWORD requested;
        DWORD completed = 0;
        if (size > (size_t)MAXDWORD) {
            requested = MAXDWORD;
        } else {
            requested = (DWORD)size;
        }
        memset(&overlapped, 0, sizeof(overlapped));
        overlapped.Offset = (DWORD)(offset & 0xffffffffU);
        overlapped.OffsetHigh = (DWORD)(offset >> 32);
        if (!ReadFile((HANDLE)handle_value, buffer, requested, &completed, &overlapped)) {
            errno = EIO;
            return -1;
        }
        *read_size = (size_t)completed;
        return 0;
    }
#else
    {
        ssize_t result;
        do {
            result = pread((int)handle_value, buffer, size, (off_t)offset);
        } while (result < 0 && errno == EINTR);
        if (result < 0) {
            return -1;
        }
        *read_size = (size_t)result;
        return 0;
    }
#endif
}

uint64_t vipr_file_size(const char *path, int *ok) {
#if defined(_WIN32)
    WIN32_FILE_ATTRIBUTE_DATA attributes;
    ULARGE_INTEGER size;
    wchar_t *wide_path = vipr_utf8_to_wide(path);
    if (wide_path == NULL) {
        *ok = 0;
        return 0;
    }
    if (!GetFileAttributesExW(wide_path, GetFileExInfoStandard, &attributes)) {
        free(wide_path);
        errno = EIO;
        *ok = 0;
        return 0;
    }
    free(wide_path);
    size.LowPart = attributes.nFileSizeLow;
    size.HighPart = attributes.nFileSizeHigh;
    *ok = 1;
    return size.QuadPart;
#else
    struct stat info;
    if (stat(path, &info) != 0 || info.st_size < 0) {
        *ok = 0;
        return 0;
    }
    *ok = 1;
    return (uint64_t)info.st_size;
#endif
}

int vipr_map_file(const char *path, vipr_mapped_file *mapped, char *error_buffer, uint64_t error_buffer_size) {
    int ok = 0;
    memset(mapped, 0, sizeof(*mapped));
#if !defined(_WIN32)
    mapped->file = -1;
#endif
    mapped->size = vipr_file_size(path, &ok);
    if (!ok) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "stat reference file");
        return -1;
    }
    if (mapped->size > (uint64_t)SIZE_MAX) {
        vipr_set_error(error_buffer, error_buffer_size, "reference file is too large for this architecture's address space");
        return -1;
    }
    if (mapped->size == 0) {
        mapped->data = NULL;
        return 0;
    }
#if defined(_WIN32)
    {
        wchar_t *wide_path = vipr_utf8_to_wide(path);
        HANDLE file;
        if (wide_path == NULL) {
            vipr_set_errno_error(error_buffer, error_buffer_size, "convert reference path to UTF-16");
            return -1;
        }
        file = CreateFileW(wide_path, GENERIC_READ, FILE_SHARE_READ, NULL, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
        free(wide_path);
        if (file == INVALID_HANDLE_VALUE) {
            vipr_set_error(error_buffer, error_buffer_size, "open reference file failed");
            return -1;
        }
        mapped->file = file;
    }
    mapped->mapping = CreateFileMappingA((HANDLE)mapped->file, NULL, PAGE_READONLY, 0, 0, NULL);
    if (mapped->mapping == NULL) {
        CloseHandle((HANDLE)mapped->file);
        mapped->file = NULL;
        vipr_set_error(error_buffer, error_buffer_size, "create reference file mapping failed");
        return -1;
    }
    mapped->data = MapViewOfFile((HANDLE)mapped->mapping, FILE_MAP_READ, 0, 0, 0);
    if (mapped->data == NULL) {
        CloseHandle((HANDLE)mapped->mapping);
        CloseHandle((HANDLE)mapped->file);
        mapped->mapping = NULL;
        mapped->file = NULL;
        vipr_set_error(error_buffer, error_buffer_size, "map reference file failed");
        return -1;
    }
#else
    mapped->file = open(path, O_RDONLY);
    if (mapped->file < 0) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "open reference file");
        return -1;
    }
    mapped->data = mmap(NULL, (size_t)mapped->size, PROT_READ, MAP_PRIVATE, mapped->file, 0);
    if (mapped->data == MAP_FAILED) {
        mapped->data = NULL;
        close(mapped->file);
        mapped->file = -1;
        vipr_set_errno_error(error_buffer, error_buffer_size, "map reference file");
        return -1;
    }
#endif
    return 0;
}

int vipr_map_handle(uintptr_t handle_value, vipr_mapped_file *mapped, char *error_buffer, uint64_t error_buffer_size) {
    memset(mapped, 0, sizeof(*mapped));
#if defined(_WIN32)
    {
        LARGE_INTEGER size;
        HANDLE file = vipr_duplicate_handle(handle_value);
        if (file == NULL) {
            vipr_set_errno_error(error_buffer, error_buffer_size, "duplicate reference handle");
            return -1;
        }
        if (!GetFileSizeEx(file, &size) || size.QuadPart < 0) {
            CloseHandle(file);
            vipr_set_error(error_buffer, error_buffer_size, "inspect reference handle failed");
            return -1;
        }
        mapped->file = file;
        mapped->size = (uint64_t)size.QuadPart;
        if (mapped->size == 0) {
            return 0;
        }
        if (mapped->size > (uint64_t)SIZE_MAX) {
            CloseHandle(file);
            mapped->file = NULL;
            vipr_set_error(error_buffer, error_buffer_size, "reference file is too large for this architecture's address space");
            return -1;
        }
        mapped->mapping = CreateFileMappingA(file, NULL, PAGE_READONLY, 0, 0, NULL);
        if (mapped->mapping == NULL) {
            CloseHandle(file);
            mapped->file = NULL;
            vipr_set_error(error_buffer, error_buffer_size, "create reference handle mapping failed");
            return -1;
        }
        mapped->data = MapViewOfFile((HANDLE)mapped->mapping, FILE_MAP_READ, 0, 0, 0);
        if (mapped->data == NULL) {
            CloseHandle((HANDLE)mapped->mapping);
            CloseHandle(file);
            mapped->mapping = NULL;
            mapped->file = NULL;
            vipr_set_error(error_buffer, error_buffer_size, "map reference handle failed");
            return -1;
        }
    }
#else
    {
        struct stat info;
        int file = dup((int)handle_value);
        if (file < 0) {
            vipr_set_errno_error(error_buffer, error_buffer_size, "duplicate reference descriptor");
            return -1;
        }
        mapped->file = file;
        if (fstat(file, &info) != 0 || info.st_size < 0) {
            close(file);
            mapped->file = -1;
            vipr_set_errno_error(error_buffer, error_buffer_size, "inspect reference descriptor");
            return -1;
        }
        mapped->size = (uint64_t)info.st_size;
        if (mapped->size == 0) {
            return 0;
        }
        if (mapped->size > (uint64_t)SIZE_MAX) {
            close(file);
            mapped->file = -1;
            vipr_set_error(error_buffer, error_buffer_size, "reference file is too large for this architecture's address space");
            return -1;
        }
        mapped->data = mmap(NULL, (size_t)mapped->size, PROT_READ, MAP_PRIVATE, file, 0);
        if (mapped->data == MAP_FAILED) {
            mapped->data = NULL;
            close(file);
            mapped->file = -1;
            vipr_set_errno_error(error_buffer, error_buffer_size, "map reference descriptor");
            return -1;
        }
    }
#endif
    return 0;
}

void vipr_unmap_file(vipr_mapped_file *mapped) {
    if (mapped->size == 0) {
#if defined(_WIN32)
        if (mapped->file != NULL) {
            CloseHandle((HANDLE)mapped->file);
        }
#else
        if (mapped->file >= 0) {
            close(mapped->file);
        }
#endif
        memset(mapped, 0, sizeof(*mapped));
        return;
    }
#if defined(_WIN32)
    if (mapped->data != NULL) {
        UnmapViewOfFile(mapped->data);
    }
    if (mapped->mapping != NULL) {
        CloseHandle((HANDLE)mapped->mapping);
    }
    if (mapped->file != NULL) {
        CloseHandle((HANDLE)mapped->file);
    }
#else
    if (mapped->data != NULL) {
        munmap(mapped->data, (size_t)mapped->size);
    }
    if (mapped->file >= 0) {
        close(mapped->file);
    }
#endif
    memset(mapped, 0, sizeof(*mapped));
#if !defined(_WIN32)
    mapped->file = -1;
#endif
}
