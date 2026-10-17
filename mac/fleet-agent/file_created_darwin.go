package main

import (
	"os"
	"syscall"
	"time"
)

func fileCreatedAt(info os.FileInfo) int64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Birthtimespec.Sec > 0 {
		return time.Unix(stat.Birthtimespec.Sec, stat.Birthtimespec.Nsec).UnixMilli()
	}
	return 0
}
