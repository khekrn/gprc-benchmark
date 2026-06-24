package com.beam.bench

/**
 * 32-bit FNV-1a checksum, byte-identical to Go's `hash/fnv.New32a()` and every
 * other stack. Int arithmetic wraps mod 2^32, matching uint32 semantics, so all
 * stacks compute the same checksum for the same bytes.
 *
 * Reference vectors: fnv1a32("") = 2166136261, fnv1a32("a") = 0xe40c292c,
 * fnv1a32("foobar") = 0xbf9cf968.
 */
internal fun fnv1a32(bytes: ByteArray): Int {
    var hash = -0x7ee3623b // 2166136261 (FNV offset basis) as signed Int
    for (b in bytes) {
        hash = hash xor (b.toInt() and 0xff)
        hash *= 0x01000193 // FNV prime (16777619), Int multiply wraps mod 2^32
    }
    return hash
}
