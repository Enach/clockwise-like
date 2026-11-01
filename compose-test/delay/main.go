//go:build linux

// Command delay is a test double for PAC-81 Part C (AC-C3): it waits for the
// given duration, then replaces itself with the given program. Wrapped
// around the backend binary in a test image, it makes the backend start
// listening as late as a long startup migration would.
//
//	delay 90s /server
package main

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: delay DURATION PROGRAM [ARGS...]")
		os.Exit(2)
	}
	d, err := time.ParseDuration(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "delay: simulating a %s startup migration\n", d)
	time.Sleep(d)
	err = syscall.Exec(os.Args[2], os.Args[2:], os.Environ())
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
