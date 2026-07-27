#include "native.h"
#include "native_internal.h"
#include "_cgo_export.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

typedef struct vipr_compression_input {
    FILE *file;
    uintptr_t handle;
    uint64_t offset;
    uint64_t length;
    uint64_t position;
} vipr_compression_input;

static int vipr_read_compression_input(
    vipr_compression_input *input,
    void *buffer,
    size_t request,
    size_t *read_size,
    char *error_buffer,
    uint64_t error_buffer_size) {
    if (request == 0) {
        *read_size = 0;
        return 0;
    }
    if (input->file != NULL) {
        *read_size = fread(buffer, 1, request, input->file);
        if (*read_size != request) {
            if (ferror(input->file)) {
                vipr_set_errno_error(error_buffer, error_buffer_size, "read compression input");
            } else {
                vipr_set_error(error_buffer, error_buffer_size, "compression input changed size while being read");
            }
            return -1;
        }
        return 0;
    }
    if (vipr_read_at(input->handle, input->offset + input->position, buffer, request, read_size) != 0) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "read compression input segment");
        return -1;
    }
    if (*read_size != request) {
        vipr_set_error(error_buffer, error_buffer_size, "compression input segment is truncated");
        return -1;
    }
    return 0;
}

static int vipr_compress_input(
    vipr_compression_input *input,
    const char *output_path,
    int compression_level,
    uintptr_t progress_handle,
    char *error_buffer,
    uint64_t error_buffer_size) {
    int result = -1;
    FILE *output = NULL;
    ZSTD_CCtx *context = NULL;
    void *input_buffer = NULL;
    void *output_buffer = NULL;

    output = vipr_open_write(output_path);
    if (output == NULL) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "create compressed output");
        goto cleanup;
    }
    context = ZSTD_createCCtx();
    if (context == NULL) {
        vipr_set_error(error_buffer, error_buffer_size, "create zstd compression context failed");
        goto cleanup;
    }

    if (vipr_set_parameter(context, ZSTD_c_contentSizeFlag, 1, error_buffer, error_buffer_size) != 0 ||
        vipr_set_parameter(context, ZSTD_c_checksumFlag, 1, error_buffer, error_buffer_size) != 0 ||
        vipr_set_parameter(context, ZSTD_c_compressionLevel, compression_level, error_buffer, error_buffer_size) != 0) {
        goto cleanup;
    }
    {
        size_t code = ZSTD_CCtx_setPledgedSrcSize(context, input->length);
        if (ZSTD_isError(code)) {
            vipr_set_zstd_error(error_buffer, error_buffer_size, "set compression input size", code);
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
        int finished = 0;
        while (!finished) {
            uint64_t remaining = input->length - input->position;
            size_t request = remaining > VIPR_IO_BUFFER_SIZE ? VIPR_IO_BUFFER_SIZE : (size_t)remaining;
            size_t read_size = 0;
            ZSTD_EndDirective directive;
            ZSTD_inBuffer in;

            if (vipr_read_compression_input(input, input_buffer, request, &read_size, error_buffer, error_buffer_size) != 0) {
                goto cleanup;
            }
            input->position += read_size;
            directive = input->position == input->length ? ZSTD_e_end : ZSTD_e_continue;
            in.src = input_buffer;
            in.size = read_size;
            in.pos = 0;

            do {
                ZSTD_outBuffer out;
                size_t frame_remaining;
                out.dst = output_buffer;
                out.size = ZSTD_CStreamOutSize();
                out.pos = 0;
                frame_remaining = ZSTD_compressStream2(context, &out, &in, directive);
                if (ZSTD_isError(frame_remaining)) {
                    vipr_set_zstd_error(error_buffer, error_buffer_size, "compress payload", frame_remaining);
                    goto cleanup;
                }
                if (out.pos > 0 && fwrite(output_buffer, 1, out.pos, output) != out.pos) {
                    vipr_set_errno_error(error_buffer, error_buffer_size, "write compressed output");
                    goto cleanup;
                }
                if (directive == ZSTD_e_end && frame_remaining == 0) {
                    finished = 1;
                }
            } while (in.pos < in.size || (directive == ZSTD_e_end && !finished));

            viprGoProgress(progress_handle, input->position, input->length);
        }
    }

    if (fflush(output) != 0) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "flush compressed output");
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
        vipr_set_errno_error(error_buffer, error_buffer_size, "close compressed output");
        result = -1;
    }
    return result;
}

int vipr_compress_file(
    const char *input_path,
    const char *output_path,
    int compression_level,
    uintptr_t progress_handle,
    char *error_buffer,
    uint64_t error_buffer_size) {
    int size_ok = 0;
    uint64_t input_size = vipr_file_size(input_path, &size_ok);
    FILE *input_file = NULL;
    int result;
    vipr_compression_input input;

    if (!size_ok) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "stat compression input");
        return -1;
    }
    input_file = vipr_open_read(input_path);
    if (input_file == NULL) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "open compression input");
        return -1;
    }

    input.file = input_file;
    input.handle = 0;
    input.offset = 0;
    input.length = input_size;
    input.position = 0;
    result = vipr_compress_input(&input, output_path, compression_level, progress_handle, error_buffer, error_buffer_size);
    if (fclose(input_file) != 0 && result == 0) {
        vipr_set_errno_error(error_buffer, error_buffer_size, "close compression input");
        result = -1;
    }
    return result;
}

int vipr_compress_segment(
    uintptr_t input_handle,
    uint64_t input_offset,
    uint64_t input_length,
    const char *output_path,
    int compression_level,
    uintptr_t progress_handle,
    char *error_buffer,
    uint64_t error_buffer_size) {
    vipr_compression_input input;
    if (input_offset > UINT64_MAX - input_length) {
        vipr_set_error(error_buffer, error_buffer_size, "compression input segment overflows");
        return -1;
    }
    input.file = NULL;
    input.handle = input_handle;
    input.offset = input_offset;
    input.length = input_length;
    input.position = 0;
    return vipr_compress_input(&input, output_path, compression_level, progress_handle, error_buffer, error_buffer_size);
}
