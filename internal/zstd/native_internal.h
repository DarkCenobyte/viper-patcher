#ifndef VIPER_PATCHER_NATIVE_INTERNAL_H
#define VIPER_PATCHER_NATIVE_INTERNAL_H

#define ZSTD_STATIC_LINKING_ONLY
#include <zstd.h>

#include <stdint.h>
#include <stdio.h>

#define VIPR_IO_BUFFER_SIZE (1024U * 1024U)

typedef struct {
    void *data;
    uint64_t size;
#if defined(_WIN32)
    void *file;
    void *mapping;
#else
    int file;
#endif
} vipr_mapped_file;

FILE *vipr_open_read(const char *path);
FILE *vipr_open_write(const char *path);
FILE *vipr_open_write_handle(uintptr_t handle);
int vipr_seek(FILE *file, uint64_t offset);
uint64_t vipr_file_size(const char *path, int *ok);
int vipr_map_file(const char *path, vipr_mapped_file *mapped, char *error_buffer, uint64_t error_buffer_size);
void vipr_unmap_file(vipr_mapped_file *mapped);

void vipr_set_error(char *buffer, uint64_t size, const char *message);
void vipr_set_errno_error(char *buffer, uint64_t size, const char *operation);
void vipr_set_zstd_error(char *buffer, uint64_t size, const char *operation, size_t code);
size_t vipr_clamp_size_t(uint64_t value);
int vipr_set_parameter(ZSTD_CCtx *context, ZSTD_cParameter parameter, int value, char *error_buffer, uint64_t error_buffer_size);

#endif
