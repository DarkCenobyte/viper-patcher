#include "native.h"
#include "native_internal.h"
#include "_cgo_export.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

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

    input = vipr_open_read(target_path);
    if (input == NULL) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "open target file");
        goto cleanup;
    }
    output = vipr_open_write(output_path);
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
    if (output != NULL && fclose(output) != 0 && result == 0) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "close differential file");
        result = -1;
    }
    if (input != NULL && fclose(input) != 0 && result == 0) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "close target file");
        result = -1;
    }
    vipr_unmap_file(&reference);
    return result;
}
