//go:build !darwin

package privileged

func defaultClientUID() int {
	return -1
}
