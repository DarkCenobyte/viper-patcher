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

int vipr_decompress_segment(
    const char *reference_path,
    const char *patch_path,
    uint64_t patch_offset,
    uint64_t patch_length,
    uintptr_t output_handle,
    uint64_t expected_output_size,
    uintptr_t progress_handle,
    char *error_buffer,
    uint64_t error_buffer_size);

#ifdef __cplusplus
}
#endif

#endif
