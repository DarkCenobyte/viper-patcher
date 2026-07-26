#include "native.h"
#include "native_internal.h"
#include "_cgo_export.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

int vipr_decompress_segment(
    const char *reference_path,
    const char *patch_path,
    uint64_t patch_offset,
    uint64_t patch_length,
    uintptr_t output_handle,
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
    input = vipr_open_read(patch_path);
    if (input == NULL) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "open patch file");
        goto cleanup;
    }
    if (vipr_seek(input, patch_offset) != 0) {
        vipr_set_error(error_buffer, error_buffer_size, "seek to differential segment failed");
        goto cleanup;
    }
    output = vipr_open_write_handle(output_handle);
    if (output == NULL) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "duplicate patched output handle");
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
        size_t code;
        if (required_window < minimum_window) {
            required_window = minimum_window;
        }
        code = ZSTD_DCtx_setMaxWindowSize(context, vipr_clamp_size_t(required_window));
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
                if (produced > expected_output_size || (uint64_t)out.pos > expected_output_size - produced) {
                    vipr_set_error(error_buffer, error_buffer_size, "decompressed output exceeds declared size");
                    goto cleanup;
                }
                if (out.pos > 0 && fwrite(output_buffer, 1, out.pos, output) != out.pos) {
                    vipr_set_errno_error(error_buffer, error_buffer_size, "write patched output file");
                    goto cleanup;
                }
                produced += (uint64_t)out.pos;
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
    if (output != NULL && fclose(output) != 0 && result == 0) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "close patched output file");
        result = -1;
    }
    if (input != NULL && fclose(input) != 0 && result == 0) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "close patch file");
        result = -1;
    }
    vipr_unmap_file(&reference);
    return result;
}
