#include "native.h"
#include "native_internal.h"

#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

void vipr_set_error(char *buffer, uint64_t size, const char *message) {
    if (buffer == NULL || size == 0) {
        return;
    }
    snprintf(buffer, (size_t)size, "%s", message != NULL ? message : "unknown error");
    buffer[size - 1] = '\0';
}

void vipr_set_errno_error(char *buffer, uint64_t size, const char *operation) {
    char message[512];
    snprintf(message, sizeof(message), "%s: %s", operation, strerror(errno));
    vipr_set_error(buffer, size, message);
}

void vipr_set_zstd_error(char *buffer, uint64_t size, const char *operation, size_t code) {
    char message[512];
    snprintf(message, sizeof(message), "%s: %s", operation, ZSTD_getErrorName(code));
    vipr_set_error(buffer, size, message);
}

size_t vipr_clamp_size_t(uint64_t value) {
    if (value > (uint64_t)SIZE_MAX) {
        return SIZE_MAX;
    }
    return (size_t)value;
}

int vipr_set_parameter(ZSTD_CCtx *context, ZSTD_cParameter parameter, int value, char *error_buffer, uint64_t error_buffer_size) {
    size_t code = ZSTD_CCtx_setParameter(context, parameter, value);
    if (ZSTD_isError(code)) {
        vipr_set_zstd_error(error_buffer, error_buffer_size, "configure zstd compression", code);
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
