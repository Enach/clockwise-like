// Package other is outside the targeted set entirely.
package other

import "os"

func Clean() {
	_ = os.Remove("x")
	f, _ := os.Open("y")
	_ = f
}
