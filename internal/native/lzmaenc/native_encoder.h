#ifndef I7Z_NATIVE_LZMA_ENCODER_H
#define I7Z_NATIVE_LZMA_ENCODER_H

#include <stddef.h>
#include <stdint.h>

enum
{
  I7Z_LZMA_ENCODE_FORMAT_LZMA = 0,
  I7Z_LZMA_ENCODE_FORMAT_LZMA2 = 1
};

enum
{
  I7Z_LZMA_ENCODE_ERROR_PARAMETER = -1,
  I7Z_LZMA_ENCODE_ERROR_MEMORY = -2,
  I7Z_LZMA_ENCODE_ERROR_MEMORY_LIMIT = -3
};

typedef struct I7zLzmaEncoder I7zLzmaEncoder;

typedef struct
{
  int result;
  int memory_limit_hit;
  uint64_t peak_memory;
} I7zLzmaEncodeResult;

I7zLzmaEncoder *i7z_lzma_encoder_create(
    int format,
    int level,
    uint32_t dictionary_size,
    int threads,
    int end_marker,
    int lc,
    int lp,
    int pb,
    uint64_t expected_size,
    int expected_size_defined,
    uint64_t max_memory,
    uint8_t *properties,
    size_t *properties_size,
    int *error_code);

I7zLzmaEncodeResult i7z_lzma_encoder_run(
    I7zLzmaEncoder *encoder,
    uintptr_t callback_handle);

void i7z_lzma_encoder_destroy(I7zLzmaEncoder *encoder);
void i7z_lzma_encoder_prepare_hardware(void);

#endif
