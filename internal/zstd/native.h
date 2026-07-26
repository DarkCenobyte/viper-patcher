#ifndef VIPER_PATCHER_NATIVE_H
#define VIPER_PATCHER_NATIVE_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

const char *vipr_zstd_version(void);
int vipr_zstd_min_level(void);
int vipr_zstd_max_level(void);

int vipr_compress_file(
    const char *reference_path,
    const char *target_path,
    const char *output_path,
    int compression_level,
    uintptr_t progress_handle,
    char *error_buffer,
    uint64_t error_buffer_size);

typedef struct vipr_decoder vipr_decoder;

vipr_decoder *vipr_decoder_create(void);
void vipr_decoder_free(vipr_decoder *decoder);

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
    uint64_t error_buffer_size);

#ifdef __cplusplus
}
#endif

#endif
