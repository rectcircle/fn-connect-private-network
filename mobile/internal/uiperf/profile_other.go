//go:build !android

package uiperf

import "time"

func Enabled() bool                   { return false }
func Mark(string, time.Duration, int) {}
