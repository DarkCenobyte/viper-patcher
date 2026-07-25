#define ZSTD_STATIC_LINKING_ONLY
#include <zstd.h>

#include "native.h"
#include "_cgo_export.h"

#include <errno.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if defined(_WIN32)
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <wchar.h>
#else
#include <fcntl.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>
#endif

#define VIPR_IO_BUFFER_SIZE (1024U * 1024U)

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
#else
static FILE *vipr_fopen_utf8(const char *path, const char *mode) {
    return fopen(path, mode);
}
#endif

typedef struct {
    void *data;
    uint64_t size;
#if defined(_WIN32)
    HANDLE file;
    HANDLE mapping;
#else
    int file;
#endif
} vipr_mapped_file;

static void vipr_set_error(char *buffer, uint64_t size, const char *message) {
    if (buffer == NULL || size == 0) {
        return;
    }
    snprintf(buffer, (size_t)size, "%s", message != NULL ? message : "unknown error");
    buffer[size - 1] = '\0';
}

static void vipr_set_errno_error(char *buffer, uint64_t size, const char *operation) {
    char message[512];
    snprintf(message, sizeof(message), "%s: %s", operation, strerror(errno));
    vipr_set_error(buffer, size, message);
}

static void vipr_set_zstd_error(char *buffer, uint64_t size, const char *operation, size_t code) {
    char message[512];
    snprintf(message, sizeof(message), "%s: %s", operation, ZSTD_getErrorName(code));
    vipr_set_error(buffer, size, message);
}

static int vipr_seek(FILE *file, uint64_t offset) {
#if defined(_WIN32)
    if (offset > (uint64_t)LLONG_MAX) {
        return -1;
    }
    return _fseeki64(file, (__int64)offset, SEEK_SET);
#else
    if (offset > (uint64_t)LLONG_MAX) {
        return -1;
    }
    return fseeko(file, (off_t)offset, SEEK_SET);
#endif
}

static uint64_t vipr_file_size(const char *path, int *ok) {
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

static int vipr_map_file(const char *path, vipr_mapped_file *mapped, char *error_buffer, uint64_t error_buffer_size) {
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
        if (wide_path == NULL) {
            vipr_set_errno_error(error_buffer, error_buffer_size, "convert reference path to UTF-16");
            return -1;
        }
        mapped->file = CreateFileW(wide_path, GENERIC_READ, FILE_SHARE_READ, NULL, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
        free(wide_path);
    }
    if (mapped->file == INVALID_HANDLE_VALUE) {
        mapped->file = NULL;
        vipr_set_error(error_buffer, error_buffer_size, "open reference file failed");
        return -1;
    }
    mapped->mapping = CreateFileMappingA(mapped->file, NULL, PAGE_READONLY, 0, 0, NULL);
    if (mapped->mapping == NULL) {
        CloseHandle(mapped->file);
        mapped->file = NULL;
        vipr_set_error(error_buffer, error_buffer_size, "create reference file mapping failed");
        return -1;
    }
    mapped->data = MapViewOfFile(mapped->mapping, FILE_MAP_READ, 0, 0, 0);
    if (mapped->data == NULL) {
        CloseHandle(mapped->mapping);
        CloseHandle(mapped->file);
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

static void vipr_unmap_file(vipr_mapped_file *mapped) {
    if (mapped->size == 0) {
        return;
    }
#if defined(_WIN32)
    if (mapped->data != NULL) {
        UnmapViewOfFile(mapped->data);
    }
    if (mapped->mapping != NULL) {
        CloseHandle(mapped->mapping);
    }
    if (mapped->file != NULL) {
        CloseHandle(mapped->file);
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
}

static unsigned vipr_high_bit64(uint64_t value) {
    unsigned result = 0;
    while (value > 1) {
        value >>= 1;
        result++;
    }
    return result;
}

static unsigned vipr_cycle_log(unsigned chain_log, ZSTD_strategy strategy) {
    unsigned bt_scale = ((unsigned)strategy >= (unsigned)ZSTD_btlazy2);
    return chain_log - bt_scale;
}

static size_t vipr_clamp_size_t(uint64_t value) {
    if (value > (uint64_t)SIZE_MAX) {
        return SIZE_MAX;
    }
    return (size_t)value;
}

static int vipr_set_parameter(ZSTD_CCtx *context, ZSTD_cParameter parameter, int value, char *error_buffer, uint64_t error_buffer_size) {
    size_t code = ZSTD_CCtx_setParameter(context, parameter, value);
    if (ZSTD_isError(code)) {
        vipr_set_zstd_error(error_buffer, error_buffer_size, "configure zstd patch-from mode", code);
        return -1;
    }
    return 0;
}

const char *vipr_zstd_version(void) {
    return ZSTD_versionString();
}

int vipr_zstd_min_level(void) {
    return ZSTD_minCLevel();
}

int vipr_zstd_max_level(void) {
    return ZSTD_maxCLevel();
}

int vipr_compress_file(
    const char *reference_path,
    const char *target_path,
    const char *output_path,
    int compression_level,
    uintptr_t progress_handle,
    char *error_buffer,
    uint64_t error_buffer_size) {
    int result = -1;
    int size_ok = 0;
    uint64_t target_size = vipr_file_size(target_path, &size_ok);
    vipr_mapped_file reference;
    FILE *input = NULL;
    FILE *output = NULL;
    ZSTD_CCtx *context = NULL;
    void *input_buffer = NULL;
    void *output_buffer = NULL;

    if (!size_ok) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "stat target file");
        return -1;
    }
    if (vipr_map_file(reference_path, &reference, error_buffer, error_buffer_size) != 0) {
        return -1;
    }

    #if defined(_WIN32)
    input = vipr_fopen_utf8(target_path, L"rb");
#else
    input = vipr_fopen_utf8(target_path, "rb");
#endif
    if (input == NULL) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "open target file");
        goto cleanup;
    }
    #if defined(_WIN32)
    output = vipr_fopen_utf8(output_path, L"wb");
#else
    output = vipr_fopen_utf8(output_path, "wb");
#endif
    if (output == NULL) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "create differential file");
        goto cleanup;
    }
    context = ZSTD_createCCtx();
    if (context == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "create zstd compression context failed");
        goto cleanup;
    }

    {
        size_t target_hint = vipr_clamp_size_t(target_size);
        size_t reference_hint = vipr_clamp_size_t(reference.size);
        ZSTD_compressionParameters parameters = ZSTD_getCParams(compression_level, target_hint, reference_hint);
        unsigned file_window_log = vipr_high_bit64(target_size) + 1;
        int enable_ldm = 0;

        if (file_window_log < ZSTD_WINDOWLOG_MIN) {
            file_window_log = ZSTD_WINDOWLOG_MIN;
        }
        if (file_window_log > ZSTD_WINDOWLOG_MAX) {
            file_window_log = ZSTD_WINDOWLOG_MAX;
        }
        parameters.windowLog = file_window_log;
        if (file_window_log > vipr_cycle_log(parameters.chainLog, parameters.strategy)) {
            enable_ldm = 1;
        }

        if (vipr_set_parameter(context, ZSTD_c_contentSizeFlag, 1, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_dictIDFlag, 1, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_checksumFlag, 1, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_compressionLevel, compression_level, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_enableLongDistanceMatching, enable_ldm, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_windowLog, (int)parameters.windowLog, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_chainLog, (int)parameters.chainLog, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_hashLog, (int)parameters.hashLog, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_searchLog, (int)parameters.searchLog, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_minMatch, (int)parameters.minMatch, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_targetLength, (int)parameters.targetLength, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_strategy, (int)parameters.strategy, error_buffer, error_buffer_size) != 0 ||
            vipr_set_parameter(context, ZSTD_c_enableDedicatedDictSearch, 1, error_buffer, error_buffer_size) != 0) {
            goto cleanup;
        }
    }

    {
        size_t code = ZSTD_CCtx_refPrefix(context, reference.data, (size_t)reference.size);
        if (ZSTD_isError(code)) {
            vipr_set_zstd_error(error_buffer, error_buffer_size, "attach patch reference", code);
            goto cleanup;
        }
        code = ZSTD_CCtx_setPledgedSrcSize(context, target_size);
        if (ZSTD_isError(code)) {
            vipr_set_zstd_error(error_buffer, error_buffer_size, "set target size", code);
            goto cleanup;
        }
    }

    input_buffer = malloc(VIPR_IO_BUFFER_SIZE);
    output_buffer = malloc(ZSTD_CStreamOutSize());
    if (input_buffer == NULL || output_buffer == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "allocate compression buffers failed");
        goto cleanup;
    }

    {
        uint64_t processed = 0;
        int finished = 0;
        while (!finished) {
            size_t read_size = fread(input_buffer, 1, VIPR_IO_BUFFER_SIZE, input);
            ZSTD_EndDirective directive;
            ZSTD_inBuffer in;
            if (ferror(input)) {
                vipr_set_errno_error(error_buffer, error_buffer_size, "read target file");
                goto cleanup;
            }
            directive = feof(input) ? ZSTD_e_end : ZSTD_e_continue;
            in.src = input_buffer;
            in.size = read_size;
            in.pos = 0;

            do {
                ZSTD_outBuffer out;
                size_t remaining;
                out.dst = output_buffer;
                out.size = ZSTD_CStreamOutSize();
                out.pos = 0;
                remaining = ZSTD_compressStream2(context, &out, &in, directive);
                if (ZSTD_isError(remaining)) {
                    vipr_set_zstd_error(error_buffer, error_buffer_size, "compress differential", remaining);
                    goto cleanup;
                }
                if (out.pos > 0 && fwrite(output_buffer, 1, out.pos, output) != out.pos) {
                    vipr_set_errno_error(error_buffer, error_buffer_size, "write differential file");
                    goto cleanup;
                }
                if (directive == ZSTD_e_end && remaining == 0) {
                    finished = 1;
                }
            } while (in.pos < in.size || (directive == ZSTD_e_end && !finished));

            processed += read_size;
            viprGoProgress(progress_handle, processed, target_size);
        }
    }

    if (fflush(output) != 0) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "flush differential file");
        goto cleanup;
    }
    result = 0;

cleanup:
    free(output_buffer);
    free(input_buffer);
    if (context != NULL) {
        ZSTD_freeCCtx(context);
    }
    if (output != NULL) {
        fclose(output);
    }
    if (input != NULL) {
        fclose(input);
    }
    vipr_unmap_file(&reference);
    return result;
}

int vipr_decompress_segment(
    const char *reference_path,
    const char *patch_path,
    uint64_t patch_offset,
    uint64_t patch_length,
    const char *output_path,
    uint64_t expected_output_size,
    uintptr_t progress_handle,
    char *error_buffer,
    uint64_t error_buffer_size) {
    int result = -1;
    vipr_mapped_file reference;
    FILE *input = NULL;
    FILE *output = NULL;
    ZSTD_DCtx *context = NULL;
    void *input_buffer = NULL;
    void *output_buffer = NULL;

    if (patch_length == 0) {
        vipr_set_error(error_buffer, error_buffer_size, "empty differential segment");
        return -1;
    }
    if (vipr_map_file(reference_path, &reference, error_buffer, error_buffer_size) != 0) {
        return -1;
    }
    #if defined(_WIN32)
    input = vipr_fopen_utf8(patch_path, L"rb");
#else
    input = vipr_fopen_utf8(patch_path, "rb");
#endif
    if (input == NULL) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "open patch file");
        goto cleanup;
    }
    if (vipr_seek(input, patch_offset) != 0) {
        vipr_set_error(error_buffer, error_buffer_size, "seek to differential segment failed");
        goto cleanup;
    }
    #if defined(_WIN32)
    output = vipr_fopen_utf8(output_path, L"wb");
#else
    output = vipr_fopen_utf8(output_path, "wb");
#endif
    if (output == NULL) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "create patched output file");
        goto cleanup;
    }
    context = ZSTD_createDCtx();
    if (context == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "create zstd decompression context failed");
        goto cleanup;
    }

    {
        uint64_t required_window = reference.size > expected_output_size ? reference.size : expected_output_size;
        const uint64_t minimum_window = 1ULL << ZSTD_WINDOWLOG_MIN;
        if (required_window < minimum_window) {
            required_window = minimum_window;
        }
        size_t code = ZSTD_DCtx_setMaxWindowSize(context, vipr_clamp_size_t(required_window));
        if (ZSTD_isError(code)) {
            vipr_set_zstd_error(error_buffer, error_buffer_size, "configure decompression window", code);
            goto cleanup;
        }
        code = ZSTD_DCtx_refPrefix(context, reference.data, (size_t)reference.size);
        if (ZSTD_isError(code)) {
            vipr_set_zstd_error(error_buffer, error_buffer_size, "attach patch reference", code);
            goto cleanup;
        }
    }

    input_buffer = malloc(VIPR_IO_BUFFER_SIZE);
    output_buffer = malloc(ZSTD_DStreamOutSize());
    if (input_buffer == NULL || output_buffer == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "allocate decompression buffers failed");
        goto cleanup;
    }

    {
        uint64_t remaining_segment = patch_length;
        uint64_t produced = 0;
        size_t frame_remaining = 1;
        while (frame_remaining != 0) {
            size_t request = remaining_segment > VIPR_IO_BUFFER_SIZE ? VIPR_IO_BUFFER_SIZE : (size_t)remaining_segment;
            size_t read_size;
            ZSTD_inBuffer in;
            if (request == 0) {
                vipr_set_error(error_buffer, error_buffer_size, "truncated differential segment");
                goto cleanup;
            }
            read_size = fread(input_buffer, 1, request, input);
            if (read_size != request) {
                if (ferror(input)) {
                    vipr_set_errno_error(error_buffer, error_buffer_size, "read differential segment");
                } else {
                    vipr_set_error(error_buffer, error_buffer_size, "truncated differential segment");
                }
                goto cleanup;
            }
            remaining_segment -= read_size;
            in.src = input_buffer;
            in.size = read_size;
            in.pos = 0;

            while (in.pos < in.size) {
                ZSTD_outBuffer out;
                out.dst = output_buffer;
                out.size = ZSTD_DStreamOutSize();
                out.pos = 0;
                frame_remaining = ZSTD_decompressStream(context, &out, &in);
                if (ZSTD_isError(frame_remaining)) {
                    vipr_set_zstd_error(error_buffer, error_buffer_size, "decompress differential", frame_remaining);
                    goto cleanup;
                }
                if (out.pos > 0 && fwrite(output_buffer, 1, out.pos, output) != out.pos) {
                    vipr_set_errno_error(error_buffer, error_buffer_size, "write patched output file");
                    goto cleanup;
                }
                produced += out.pos;
                viprGoProgress(progress_handle, produced, expected_output_size);
                if (frame_remaining == 0 && (in.pos != in.size || remaining_segment != 0)) {
                    vipr_set_error(error_buffer, error_buffer_size, "differential segment contains trailing data");
                    goto cleanup;
                }
            }
        }
        if (remaining_segment != 0) {
            vipr_set_error(error_buffer, error_buffer_size, "differential segment was not fully consumed");
            goto cleanup;
        }
        if (produced != expected_output_size) {
            vipr_set_error(error_buffer, error_buffer_size, "patched output size does not match metadata");
            goto cleanup;
        }
    }

    if (fflush(output) != 0) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "flush patched output file");
        goto cleanup;
    }
    result = 0;

cleanup:
    free(output_buffer);
    free(input_buffer);
    if (context != NULL) {
        ZSTD_freeDCtx(context);
    }
    if (output != NULL) {
        fclose(output);
    }
    if (input != NULL) {
        fclose(input);
    }
    vipr_unmap_file(&reference);
    return result;
}
