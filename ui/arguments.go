package main

import (
	"os"
	"runtime"
)

func platformArguments() []string { return os.Args[1:] }

func uiPlatform() string { return runtime.GOOS }
