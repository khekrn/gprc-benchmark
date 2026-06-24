// Command topology prints what the Go runtime sees about this machine's
// CPU topology and scheduler configuration.
//
//	go run ./ch01/topology
package main

import (
	"fmt"
	"runtime"
)

func main() {
	fmt.Printf("NumCPU (logical CPUs Go sees) : %d\n", runtime.NumCPU())
	fmt.Printf("GOMAXPROCS (number of P's)    : %d\n", runtime.GOMAXPROCS(0))
	fmt.Printf("Live goroutines right now     : %d\n", runtime.NumGoroutine())
	fmt.Printf("Go version                    : %s\n", runtime.Version())
	fmt.Printf("GOOS/GOARCH                   : %s/%s\n", runtime.GOOS, runtime.GOARCH)

	// runtime.GOMAXPROCS(0) returns the current value without changing it.
	// On the Ryzen 5 7535HS this prints 12 (6 cores * 2 SMT threads).
}
