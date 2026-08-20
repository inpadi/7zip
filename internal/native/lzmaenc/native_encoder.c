//go:build cgo && !purego && (amd64 || arm64)

#include "native_encoder.h"

#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>

#include "../sdk7z/LzFind.h"
#include "../sdk7z/Lzma2Enc.h"

extern int i7zGoLzmaRead(uintptr_t handle, void *data, size_t *size);
extern size_t i7zGoLzmaWrite(uintptr_t handle, const void *data, size_t size);

typedef union
{
  max_align_t alignment;
  size_t size;
} I7zAllocationHeader;

typedef struct
{
  ISzAlloc interface;
  size_t limit;
  _Atomic size_t used;
  _Atomic size_t peak;
  _Atomic int limit_hit;
} I7zLimitedAllocator;

struct I7zLzmaEncoder
{
  I7zLimitedAllocator allocator;
  int format;
  CLzmaEncHandle lzma;
  CLzma2EncHandle lzma2;
};

typedef struct
{
  ISeqInStream interface;
  uintptr_t handle;
} I7zInputStream;

typedef struct
{
  ISeqOutStream interface;
  uintptr_t handle;
} I7zOutputStream;

static void i7z_update_peak(I7zLimitedAllocator *allocator, size_t value)
{
  size_t peak = atomic_load_explicit(&allocator->peak, memory_order_relaxed);
  while (peak < value && !atomic_compare_exchange_weak_explicit(
      &allocator->peak, &peak, value,
      memory_order_relaxed, memory_order_relaxed))
  {
  }
}

static void *i7z_limited_alloc(ISzAllocPtr interface, size_t size)
{
  I7zLimitedAllocator *allocator = (I7zLimitedAllocator *)(void *)interface;
  I7zAllocationHeader *allocation;
  size_t used = atomic_load_explicit(&allocator->used, memory_order_relaxed);

  if (size == 0)
    return NULL;

  for (;;)
  {
    if (size > allocator->limit - used)
    {
      atomic_store_explicit(&allocator->limit_hit, 1, memory_order_relaxed);
      return NULL;
    }
    if (atomic_compare_exchange_weak_explicit(
        &allocator->used, &used, used + size,
        memory_order_relaxed, memory_order_relaxed))
      break;
  }

  if (size > SIZE_MAX - sizeof(*allocation))
  {
    atomic_fetch_sub_explicit(&allocator->used, size, memory_order_relaxed);
    atomic_store_explicit(&allocator->limit_hit, 1, memory_order_relaxed);
    return NULL;
  }

  allocation = (I7zAllocationHeader *)malloc(sizeof(*allocation) + size);
  if (!allocation)
  {
    atomic_fetch_sub_explicit(&allocator->used, size, memory_order_relaxed);
    return NULL;
  }
  allocation->size = size;
  i7z_update_peak(allocator, used + size);
  return allocation + 1;
}

static void i7z_limited_free(ISzAllocPtr interface, void *address)
{
  I7zLimitedAllocator *allocator = (I7zLimitedAllocator *)(void *)interface;
  I7zAllocationHeader *allocation;

  if (!address)
    return;
  allocation = (I7zAllocationHeader *)address - 1;
  atomic_fetch_sub_explicit(&allocator->used, allocation->size, memory_order_relaxed);
  free(allocation);
}

static void i7z_allocator_init(I7zLimitedAllocator *allocator, uint64_t limit)
{
  allocator->interface.Alloc = i7z_limited_alloc;
  allocator->interface.Free = i7z_limited_free;
  allocator->limit = limit > SIZE_MAX ? SIZE_MAX : (size_t)limit;
  atomic_init(&allocator->used, 0);
  atomic_init(&allocator->peak, 0);
  atomic_init(&allocator->limit_hit, 0);
}

static SRes i7z_input_read(ISeqInStreamPtr interface, void *data, size_t *size)
{
  const I7zInputStream *stream = (const I7zInputStream *)(const void *)interface;
  return (SRes)i7zGoLzmaRead(stream->handle, data, size);
}

static size_t i7z_output_write(ISeqOutStreamPtr interface, const void *data, size_t size)
{
  const I7zOutputStream *stream = (const I7zOutputStream *)(const void *)interface;
  return i7zGoLzmaWrite(stream->handle, data, size);
}

static void i7z_lzma_props(
    CLzmaEncProps *properties,
    int level,
    uint32_t dictionary_size,
    int threads,
    int end_marker,
    int lc,
    int lp,
    int pb)
{
  LzmaEncProps_Init(properties);
  properties->level = level;
  properties->dictSize = dictionary_size;
  properties->numThreads = level >= 5 ? threads : 1;
  properties->writeEndMark = end_marker != 0;
  if (lc >= 0)
    properties->lc = lc;
  if (lp >= 0)
    properties->lp = lp;
  if (pb >= 0)
    properties->pb = pb;
  if (level >= 5)
  {
    properties->algo = 1;
    properties->btMode = 1;
    properties->numHashBytes = 4;
  }
}

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
    int *error_code)
{
  I7zLzmaEncoder *encoder;
  SRes result = SZ_ERROR_PARAM;

  if (error_code)
    *error_code = I7Z_LZMA_ENCODE_ERROR_PARAMETER;
  if (!error_code || !properties || !properties_size ||
      level < 0 || level > 9 || dictionary_size < (1u << 12) ||
      threads < 1 || threads > 2 || max_memory == 0 ||
      lc < -1 || lc > 8 || lp < -1 || lp > 4 || pb < -1 || pb > 4 ||
      ((lc < 0 || lp < 0 || pb < 0) && !(lc == -1 && lp == -1 && pb == -1)) ||
      (format == I7Z_LZMA_ENCODE_FORMAT_LZMA2 && lc >= 0 && lc + lp > 4))
    return NULL;
  if ((format == I7Z_LZMA_ENCODE_FORMAT_LZMA && *properties_size < LZMA_PROPS_SIZE) ||
      (format == I7Z_LZMA_ENCODE_FORMAT_LZMA2 && *properties_size < 1))
    return NULL;

  encoder = (I7zLzmaEncoder *)calloc(1, sizeof(*encoder));
  if (!encoder)
  {
    *error_code = I7Z_LZMA_ENCODE_ERROR_MEMORY;
    return NULL;
  }
  encoder->format = format;
  i7z_allocator_init(&encoder->allocator, max_memory);

  if (format == I7Z_LZMA_ENCODE_FORMAT_LZMA)
  {
    CLzmaEncProps sdk_properties;
    SizeT size = *properties_size;

    i7z_lzma_props(
        &sdk_properties, level, dictionary_size, threads, end_marker,
        lc, lp, pb);
    encoder->lzma = LzmaEnc_Create(&encoder->allocator.interface);
    if (!encoder->lzma)
      result = SZ_ERROR_MEM;
    else
      result = LzmaEnc_SetProps(encoder->lzma, &sdk_properties);
    if (result == SZ_OK && expected_size_defined)
      LzmaEnc_SetDataSize(encoder->lzma, expected_size);
    if (result == SZ_OK)
      result = LzmaEnc_WriteProperties(encoder->lzma, properties, &size);
    *properties_size = size;
  }
  else if (format == I7Z_LZMA_ENCODE_FORMAT_LZMA2)
  {
    CLzma2EncProps sdk_properties;

    Lzma2EncProps_Init(&sdk_properties);
    i7z_lzma_props(
        &sdk_properties.lzmaProps, level, dictionary_size, threads, 0,
        lc, lp, pb);
    sdk_properties.blockSize = LZMA2_ENC_PROPS_BLOCK_SIZE_SOLID;
    sdk_properties.numBlockThreads_Reduced = 1;
    sdk_properties.numBlockThreads_Max = 1;
    sdk_properties.numTotalThreads = threads;

    encoder->lzma2 = Lzma2Enc_Create(
        &encoder->allocator.interface, &encoder->allocator.interface);
    if (!encoder->lzma2)
      result = SZ_ERROR_MEM;
    else
      result = Lzma2Enc_SetProps(encoder->lzma2, &sdk_properties);
    if (result == SZ_OK && expected_size_defined)
      Lzma2Enc_SetDataSize(encoder->lzma2, expected_size);
    if (result == SZ_OK)
    {
      properties[0] = Lzma2Enc_WriteProperties(encoder->lzma2);
      *properties_size = 1;
    }
  }

  if (result != SZ_OK)
  {
    int limit_hit = atomic_load_explicit(
        &encoder->allocator.limit_hit, memory_order_relaxed);
    i7z_lzma_encoder_destroy(encoder);
    *error_code = limit_hit
        ? I7Z_LZMA_ENCODE_ERROR_MEMORY_LIMIT
        : (result == SZ_ERROR_MEM ? I7Z_LZMA_ENCODE_ERROR_MEMORY : result);
    return NULL;
  }

  *error_code = SZ_OK;
  return encoder;
}

I7zLzmaEncodeResult i7z_lzma_encoder_run(
    I7zLzmaEncoder *encoder,
    uintptr_t callback_handle)
{
  I7zLzmaEncodeResult output = { SZ_ERROR_PARAM, 0, 0 };
  I7zInputStream input;
  I7zOutputStream destination;

  if (!encoder)
    return output;

  input.interface.Read = i7z_input_read;
  input.handle = callback_handle;
  destination.interface.Write = i7z_output_write;
  destination.handle = callback_handle;

  if (encoder->format == I7Z_LZMA_ENCODE_FORMAT_LZMA)
  {
    output.result = LzmaEnc_Encode(
        encoder->lzma, &destination.interface, &input.interface, NULL,
        &encoder->allocator.interface, &encoder->allocator.interface);
  }
  else if (encoder->format == I7Z_LZMA_ENCODE_FORMAT_LZMA2)
  {
    output.result = Lzma2Enc_Encode2(
        encoder->lzma2, &destination.interface, NULL, NULL,
        &input.interface, NULL, 0, NULL);
  }

  output.memory_limit_hit = atomic_load_explicit(
      &encoder->allocator.limit_hit, memory_order_relaxed);
  output.peak_memory = (uint64_t)atomic_load_explicit(
      &encoder->allocator.peak, memory_order_relaxed);
  return output;
}

void i7z_lzma_encoder_destroy(I7zLzmaEncoder *encoder)
{
  if (!encoder)
    return;
  if (encoder->lzma)
    LzmaEnc_Destroy(
        encoder->lzma,
        &encoder->allocator.interface,
        &encoder->allocator.interface);
  if (encoder->lzma2)
    Lzma2Enc_Destroy(encoder->lzma2);
  free(encoder);
}

void i7z_lzma_encoder_prepare_hardware(void)
{
  LzFindPrepare();
}
