//go:build !darwin && !windows

package privileged

func defaultClientUID() int {
	return -1
}
