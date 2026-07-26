#include "native.h"
#include "native_internal.h"
#include "_cgo_export.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct vipr_decoder {
    ZSTD_DCtx *context;
    void *input_buffer;
    void *output_buffer;
    size_t input_capacity;
    size_t output_capacity;
};

vipr_decoder *vipr_decoder_create(void) {
    vipr_decoder *decoder = (vipr_decoder *)calloc(1, sizeof(*decoder));
    if (decoder == NULL) {
        return NULL;
    }
    decoder->context = ZSTD_createDCtx();
    decoder->input_capacity = VIPR_IO_BUFFER_SIZE;
    decoder->output_capacity = VIPR_IO_BUFFER_SIZE;
    decoder->input_buffer = malloc(decoder->input_capacity);
    decoder->output_buffer = malloc(decoder->output_capacity);
    if (decoder->context == NULL || decoder->input_buffer == NULL || decoder->output_buffer == NULL) {
        vipr_decoder_free(decoder);
        return NULL;
    }
    return decoder;
}

void vipr_decoder_free(vipr_decoder *decoder) {
    if (decoder == NULL) {
        return;
    }
    free(decoder->output_buffer);
    free(decoder->input_buffer);
    if (decoder->context != NULL) {
        ZSTD_freeDCtx(decoder->context);
    }
    free(decoder);
}

int vipr_decoder_decompress_segment(
    vipr_decoder *decoder,
    uintptr_t reference_handle,
    uintptr_t patch_handle,
    uint64_t patch_offset,
    uint64_t patch_length,
    uintptr_t output_handle,
    uint64_t expected_output_size,
    uintptr_t callback_handle,
    char *error_buffer,
    uint64_t error_buffer_size) {
    int result = -1;
    vipr_mapped_file reference;
    int reference_mapped = 0;
    FILE *output = NULL;

    memset(&reference, 0, sizeof(reference));
#if !defined(_WIN32)
    reference.file = -1;
#endif
    if (decoder == NULL || decoder->context == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "decoder is unavailable");
        return -1;
    }
    if (patch_handle == 0 || output_handle == 0) {
        vipr_set_error(error_buffer, error_buffer_size, "patch and output handles are required");
        return -1;
    }
    if (patch_length == 0) {
        vipr_set_error(error_buffer, error_buffer_size, "empty differential segment");
        return -1;
    }

    if (reference_handle != 0) {
        if (vipr_map_handle(reference_handle, &reference, error_buffer, error_buffer_size) != 0) {
            return -1;
        }
        reference_mapped = 1;
    }
    output = vipr_open_write_handle(output_handle);
    if (output == NULL) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "duplicate patched output handle");
        goto cleanup;
    }

    {
        size_t code = ZSTD_DCtx_reset(decoder->context, ZSTD_reset_session_and_parameters);
        uint64_t required_window = reference.size > expected_output_size ? reference.size : expected_output_size;
        const uint64_t minimum_window = 1ULL << ZSTD_WINDOWLOG_MIN;
        if (ZSTD_isError(code)) {
            vipr_set_zstd_error(error_buffer, error_buffer_size, "reset decompression context", code);
            goto cleanup;
        }
        if (required_window < minimum_window) {
            required_window = minimum_window;
        }
        code = ZSTD_DCtx_setMaxWindowSize(decoder->context, vipr_clamp_size_t(required_window));
        if (ZSTD_isError(code)) {
            vipr_set_zstd_error(error_buffer, error_buffer_size, "configure decompression window", code);
            goto cleanup;
        }
        if (reference_handle != 0) {
            code = ZSTD_DCtx_refPrefix(decoder->context, reference.data, (size_t)reference.size);
            if (ZSTD_isError(code)) {
                vipr_set_zstd_error(error_buffer, error_buffer_size, "attach patch reference", code);
                goto cleanup;
            }
        }
    }

    {
        uint64_t remaining_segment = patch_length;
        uint64_t produced = 0;
        size_t frame_remaining = 1;
        while (frame_remaining != 0) {
            size_t request = remaining_segment > decoder->input_capacity ? decoder->input_capacity : (size_t)remaining_segment;
            size_t read_size;
            ZSTD_inBuffer in;
            if (request == 0) {
                vipr_set_error(error_buffer, error_buffer_size, "truncated differential segment");
                goto cleanup;
            }
            if (vipr_read_at(patch_handle, patch_offset + (patch_length - remaining_segment), decoder->input_buffer, request, &read_size) != 0) {
                vipr_set_errno_error(error_buffer, error_buffer_size, "read differential segment");
                goto cleanup;
            }
            if (read_size != request) {
                vipr_set_error(error_buffer, error_buffer_size, "truncated differential segment");
                goto cleanup;
            }
            remaining_segment -= read_size;
            in.src = decoder->input_buffer;
            in.size = read_size;
            in.pos = 0;

            while (in.pos < in.size) {
                ZSTD_outBuffer out;
                out.dst = decoder->output_buffer;
                out.size = decoder->output_capacity;
                out.pos = 0;
                frame_remaining = ZSTD_decompressStream(decoder->context, &out, &in);
                if (ZSTD_isError(frame_remaining)) {
                    vipr_set_zstd_error(error_buffer, error_buffer_size, "decompress differential", frame_remaining);
                    goto cleanup;
                }
                if (produced > expected_output_size || (uint64_t)out.pos > expected_output_size - produced) {
                    vipr_set_error(error_buffer, error_buffer_size, "decompressed output exceeds declared size");
                    goto cleanup;
                }
                if (out.pos > 0) {
                    if (fwrite(decoder->output_buffer, 1, out.pos, output) != out.pos) {
                        vipr_set_errno_error(error_buffer, error_buffer_size, "write patched output file");
                        goto cleanup;
                    }
                    produced += (uint64_t)out.pos;
                    if (viprGoOutput(callback_handle, decoder->output_buffer, (uint64_t)out.pos, produced, expected_output_size) != 0) {
                        vipr_set_error(error_buffer, error_buffer_size, "output callback failed");
                        goto cleanup;
                    }
                }
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
    if (output != NULL && fclose(output) != 0 && result == 0) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "close patched output file");
        result = -1;
    }
    if (reference_mapped) {
        vipr_unmap_file(&reference);
    }
    return result;
}
