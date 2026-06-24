package com.beam.bench;

/**
 * 32-bit FNV-1a checksum, byte-identical to Go's {@code hash/fnv.New32a()} and
 * the other stacks. Java {@code int} arithmetic wraps mod 2^32, matching uint32
 * semantics, so every stack computes the same checksum for the same bytes.
 */
final class Fnv {
    private static final int OFFSET_BASIS = 0x811c9dc5; // 2166136261
    private static final int PRIME = 0x01000193;        // 16777619

    private Fnv() {
    }

    static int fnv1a32(byte[] bytes) {
        int hash = OFFSET_BASIS;
        for (byte b : bytes) {
            hash ^= (b & 0xff);
            hash *= PRIME;
        }
        return hash;
    }
}
