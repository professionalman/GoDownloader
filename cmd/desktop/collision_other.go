//go:build !windows

package main

import (
	"fmt"
	"os"
)

func showNativeCollisionError() {
	fmt.Fprintf(os.Stderr, "Another GoDownloader runtime is already using this data store.\nClose the other GoDownloader process and try again.\n")
}
