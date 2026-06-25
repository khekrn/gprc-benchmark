package main

// FNV-1a 32-bit constants. Inlined over the string (not hash/fnv.New32a) to
// avoid the per-call hash.Hash32 interface allocation and the []byte copy —
// byte-identical to go-pgx and every other stack.
const (
	fnvOffset32 uint32 = 2166136261
	fnvPrime32  uint32 = 16777619
)

func fnv1a(s string) uint32 {
	h := fnvOffset32
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= fnvPrime32
	}
	return h
}
