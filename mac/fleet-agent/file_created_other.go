//go:build !darwin

package main

import "os"

// Leave creation time unknown on platforms without birth time support.
func fileCreatedAt(_ os.FileInfo) int64 { return 0 }
