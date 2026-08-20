//go:build cgo && !purego

#include "native_decoder.h"

#include <stdlib.h>

#include "../sdk7z/Lzma2Dec.h"

// Compile the unmodified SDK sources in this translation unit. Keeping the
// source files under sdk7z/ makes their upstream provenance explicit.
#include "../sdk7z/LzmaDec.c"
#include "../sdk7z/Lzma2Dec.c"

struct I7zLzmaDecoder
{
  int format;
  CLzmaDec lzma;
  CLzma2Dec lzma2;
  uint8_t *input;
  size_t input_capacity;
};

static void *i7z_lzma_alloc(ISzAllocPtr allocator, size_t size)
{
  (void)allocator;
  return size == 0 ? NULL : malloc(size);
}

static void i7z_lzma_free(ISzAllocPtr allocator, void *address)
{
  (void)allocator;
  free(address);
}

static const ISzAlloc i7z_lzma_allocator =
{
  i7z_lzma_alloc,
  i7z_lzma_free
};

static int i7z_lzma_dictionary_size(
    int format,
    const uint8_t *properties,
    size_t properties_size,
    uint64_t *dictionary_size)
{
  if (format == I7Z_LZMA_FORMAT_LZMA)
  {
    CLzmaProps decoded;
    SRes result;

    if (properties_size != LZMA_PROPS_SIZE)
      return I7Z_LZMA_ERROR_PARAMETER;

    result = LzmaProps_Decode(&decoded, properties, (unsigned)properties_size);
    if (result != SZ_OK)
      return result;

    *dictionary_size = decoded.dicSize;
    return SZ_OK;
  }

  if (format == I7Z_LZMA_FORMAT_LZMA2)
  {
    unsigned property;

    if (properties_size != 1)
      return I7Z_LZMA_ERROR_PARAMETER;

    property = properties[0];
    if (property > 40)
      return SZ_ERROR_UNSUPPORTED;

    *dictionary_size = property == 40
        ? UINT32_MAX
        : ((uint64_t)(2u | (property & 1u)) << (property / 2u + 11u));
    return SZ_OK;
  }

  return I7Z_LZMA_ERROR_PARAMETER;
}

I7zLzmaDecoder *i7z_lzma_decoder_create(
    int format,
    const uint8_t *properties,
    size_t properties_size,
    uint64_t max_dictionary,
    size_t input_capacity,
    int *error_code)
{
  I7zLzmaDecoder *decoder;
  uint64_t dictionary_size = 0;
  int result;

  if (error_code)
    *error_code = I7Z_LZMA_ERROR_PARAMETER;

  if (!properties || !error_code || input_capacity < LZMA_REQUIRED_INPUT_MAX)
    return NULL;

  result = i7z_lzma_dictionary_size(
      format, properties, properties_size, &dictionary_size);
  if (result != SZ_OK)
  {
    *error_code = result;
    return NULL;
  }

  if (dictionary_size > max_dictionary)
  {
    *error_code = I7Z_LZMA_ERROR_DICTIONARY_LIMIT;
    return NULL;
  }

  decoder = (I7zLzmaDecoder *)calloc(1, sizeof(*decoder));
  if (!decoder)
  {
    *error_code = I7Z_LZMA_ERROR_MEMORY;
    return NULL;
  }

  decoder->input = (uint8_t *)malloc(input_capacity);
  if (!decoder->input)
  {
    free(decoder);
    *error_code = I7Z_LZMA_ERROR_MEMORY;
    return NULL;
  }

  decoder->format = format;
  decoder->input_capacity = input_capacity;

  if (format == I7Z_LZMA_FORMAT_LZMA)
  {
    LzmaDec_Construct(&decoder->lzma);
    result = LzmaDec_Allocate(
        &decoder->lzma, properties, (unsigned)properties_size,
        &i7z_lzma_allocator);
    if (result == SZ_OK)
      LzmaDec_Init(&decoder->lzma);
  }
  else
  {
    Lzma2Dec_Construct(&decoder->lzma2);
    result = Lzma2Dec_Allocate(
        &decoder->lzma2, properties[0], &i7z_lzma_allocator);
    if (result == SZ_OK)
      Lzma2Dec_Init(&decoder->lzma2);
  }

  if (result != SZ_OK)
  {
    i7z_lzma_decoder_destroy(decoder);
    *error_code = result;
    return NULL;
  }

  *error_code = SZ_OK;
  return decoder;
}

void i7z_lzma_decoder_destroy(I7zLzmaDecoder *decoder)
{
  if (!decoder)
    return;

  if (decoder->format == I7Z_LZMA_FORMAT_LZMA)
    LzmaDec_Free(&decoder->lzma, &i7z_lzma_allocator);
  else if (decoder->format == I7Z_LZMA_FORMAT_LZMA2)
    Lzma2Dec_Free(&decoder->lzma2, &i7z_lzma_allocator);

  free(decoder->input);
  decoder->input = NULL;
  free(decoder);
}

uint8_t *i7z_lzma_decoder_input(I7zLzmaDecoder *decoder)
{
  return decoder ? decoder->input : NULL;
}

I7zLzmaDecodeResult i7z_lzma_decoder_decode(
    I7zLzmaDecoder *decoder,
    size_t input_offset,
    size_t input_size,
    size_t max_output,
    int finish_end)
{
  I7zLzmaDecodeResult output = { NULL, 0, 0, I7Z_LZMA_ERROR_PARAMETER, 0 };
  SizeT old_position;
  SizeT dictionary_size;
  SizeT dictionary_limit;
  SizeT source_size;
  ELzmaStatus status = LZMA_STATUS_NOT_SPECIFIED;
  ELzmaFinishMode finish_mode = finish_end ? LZMA_FINISH_END : LZMA_FINISH_ANY;

  if (!decoder || input_offset > decoder->input_capacity ||
      input_size > decoder->input_capacity - input_offset)
    return output;

  if (decoder->format == I7Z_LZMA_FORMAT_LZMA)
  {
    dictionary_size = decoder->lzma.dicBufSize;
    if (decoder->lzma.dicPos == dictionary_size)
      decoder->lzma.dicPos = 0;
    old_position = decoder->lzma.dicPos;
  }
  else if (decoder->format == I7Z_LZMA_FORMAT_LZMA2)
  {
    dictionary_size = decoder->lzma2.decoder.dicBufSize;
    if (decoder->lzma2.decoder.dicPos == dictionary_size)
      decoder->lzma2.decoder.dicPos = 0;
    old_position = decoder->lzma2.decoder.dicPos;
  }
  else
  {
    return output;
  }

  if (max_output > dictionary_size - old_position)
    max_output = dictionary_size - old_position;
  dictionary_limit = old_position + max_output;
  source_size = input_size;

  if (decoder->format == I7Z_LZMA_FORMAT_LZMA)
  {
    output.result = LzmaDec_DecodeToDic(
        &decoder->lzma, dictionary_limit,
        decoder->input + input_offset, &source_size,
        finish_mode, &status);
    output.data = decoder->lzma.dic + old_position;
    output.produced = decoder->lzma.dicPos - old_position;
  }
  else
  {
    output.result = Lzma2Dec_DecodeToDic(
        &decoder->lzma2, dictionary_limit,
        decoder->input + input_offset, &source_size,
        finish_mode, &status);
    output.data = decoder->lzma2.decoder.dic + old_position;
    output.produced = decoder->lzma2.decoder.dicPos - old_position;
  }

  output.consumed = source_size;
  output.status = (int)status;
  return output;
}
