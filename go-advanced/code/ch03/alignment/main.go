// Command alignment shows how struct field *order* changes a struct's size
// because of alignment padding (§3.8). Same fields, different order, different
// size — and the well-ordered version packs into fewer cache lines.
//
//	go run ./ch03/alignment
//
// Tooling note: `go vet` ships `fieldalignment`
// (golang.org/x/tools/go/analysis/passes/fieldalignment) which flags these
// automatically; run `fieldalignment ./...` to auto-detect.
package main

import (
	"fmt"
	"unsafe"
)

// bad: poor field order. Each field must sit at an offset that is a multiple of
// its own alignment, so the compiler inserts padding holes.
//
//	bool(1) [pad 7] int64(8) bool(1) [pad 3] int32(4) bool(1) [pad 7]  = 32 bytes
type bad struct {
	a bool  // offset 0
	b int64 // must be 8-aligned -> 7 bytes padding before it
	c bool
	d int32 // must be 4-aligned -> 3 bytes padding before it
	e bool
}

// good: fields ordered large -> small. Padding collapses.
//
//	int64(8) int32(4) bool(1) bool(1) bool(1) [pad 1] = 16 bytes
type good struct {
	b int64
	d int32
	a bool
	c bool
	e bool
}

func main() {
	fmt.Printf("sizeof(bad)  = %2d bytes  (align %d)\n", unsafe.Sizeof(bad{}), unsafe.Alignof(bad{}))
	fmt.Printf("sizeof(good) = %2d bytes  (align %d)\n", unsafe.Sizeof(good{}), unsafe.Alignof(good{}))
	fmt.Println()
	fmt.Println("field offsets in bad:")
	var x bad
	fmt.Printf("  a@%d b@%d c@%d d@%d e@%d\n",
		unsafe.Offsetof(x.a), unsafe.Offsetof(x.b), unsafe.Offsetof(x.c),
		unsafe.Offsetof(x.d), unsafe.Offsetof(x.e))
	fmt.Println("field offsets in good:")
	var y good
	fmt.Printf("  b@%d d@%d a@%d c@%d e@%d\n",
		unsafe.Offsetof(y.b), unsafe.Offsetof(y.d), unsafe.Offsetof(y.a),
		unsafe.Offsetof(y.c), unsafe.Offsetof(y.e))
}
