//! FNV-1a 32-bit checksum — the "small CPU touch" every stack performs on the
//! payload before it writes.
//!
//! Byte-identical to go-pgx's inlined `fnv1a` and kotlin-vertx's `Fnv.kt`, so
//! all stacks compute and store the same checksum for the same payload. We do
//! NOT use the `fnv` crate: its `FnvHasher` is the 64-bit variant, and casting
//! its output down with `as u32` would silently disagree with the 32-bit hash
//! the other stacks store. A hand-rolled loop is also faster here — no
//! `Hasher` trait object, no per-call setup.

const OFFSET_BASIS: u32 = 2166136261;
const PRIME: u32 = 16777619;

/// FNV-1a 32-bit over the UTF-8 bytes of `s`. No allocation; the `wrapping_mul`
/// matches Go's `uint32` overflow semantics exactly.
#[inline]
pub fn fnv1a_32(s: &str) -> u32 {
    let mut hash = OFFSET_BASIS;
    for &byte in s.as_bytes() {
        hash ^= byte as u32;
        hash = hash.wrapping_mul(PRIME);
    }
    hash
}

#[cfg(test)]
mod tests {
    use super::fnv1a_32;

    #[test]
    fn known_vectors() {
        // Reference values for FNV-1a 32-bit (same as Go's hash/fnv New32a).
        assert_eq!(fnv1a_32(""), 2166136261);
        assert_eq!(fnv1a_32("a"), 0xe40c292c);
        assert_eq!(fnv1a_32("foobar"), 0xbf9cf968);
    }
}
