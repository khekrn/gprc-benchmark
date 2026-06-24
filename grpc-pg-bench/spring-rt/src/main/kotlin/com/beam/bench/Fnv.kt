package com.beam.bench

/**
 * 32-bit FNV-1a checksum, byte-identical to Go's `hash/fnv.New32a()` and every
 * other stack. Int arithmetic wraps mod 2^32, matching uint32 semantics.
 */
internal fun fnv1a32(bytes: ByteArray): Int {
    var hash = -0x7ee3623b // 2166136261 (FNV offset basis) as signed Int
    for (b in bytes) {
        hash = hash xor (b.toInt() and 0xff)
        hash *= 0x01000193 // FNV prime
    }
    return hash
}
