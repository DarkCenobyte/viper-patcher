#if !defined(_WIN32)
#if !defined(_FILE_OFFSET_BITS)
#define _FILE_OFFSET_BITS 64
#elif _FILE_OFFSET_BITS != 64
#error "Viper-Patcher requires 64-bit POSIX file offsets"
#endif
#if !defined(_POSIX_C_SOURCE)
#define _POSIX_C_SOURCE 200809L
#endif
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
#include <sys/stat.h>
#include <unistd.h>
#endif

#if !defined(_WIN32)
_Static_assert(
    sizeof(off_t) >= sizeof(int64_t),
    "Viper-Patcher requires 64-bit POSIX file offsets");
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
