#ifndef I7Z_NATIVE_LZMA_DECODER_H
#define I7Z_NATIVE_LZMA_DECODER_H

#include <stddef.h>
#include <stdint.h>

enum
{
  I7Z_LZMA_FORMAT_LZMA = 0,
  I7Z_LZMA_FORMAT_LZMA2 = 1
};

enum
{
  I7Z_LZMA_ERROR_PARAMETER = -1,
  I7Z_LZMA_ERROR_MEMORY = -2,
  I7Z_LZMA_ERROR_DICTIONARY_LIMIT = -3
};

typedef struct I7zLzmaDecoder I7zLzmaDecoder;

typedef struct
{
  const uint8_t *data;
  size_t produced;
  size_t consumed;
  int result;
  int status;
} I7zLzmaDecodeResult;

I7zLzmaDecoder *i7z_lzma_decoder_create(
    int format,
    const uint8_t *properties,
    size_t properties_size,
    uint64_t max_dictionary,
    size_t input_capacity,
    int *error_code);

void i7z_lzma_decoder_destroy(I7zLzmaDecoder *decoder);

uint8_t *i7z_lzma_decoder_input(I7zLzmaDecoder *decoder);

I7zLzmaDecodeResult i7z_lzma_decoder_decode(
    I7zLzmaDecoder *decoder,
    size_t input_offset,
    size_t input_size,
    size_t max_output,
    int finish_end);

#endif
