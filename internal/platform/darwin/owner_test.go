//go:build darwin

package darwin

import "testing"

func TestOwnerLeaseIsExclusive(t *testing.T) {
	directory := t.TempDir()
	first, err := acquireOwnerLease(
		directory,
		"00112233445566778899aabbccddeeff",
	)
	if err != nil {
		t.Fatalf("acquire first lease: %v", err)
	}
	defer first.Close()
	if second, err := acquireOwnerLease(
		directory,
		"ffeeddccbbaa99887766554433221100",
	); err == nil {
		second.Close()
		t.Fatal("second owner lease unexpectedly succeeded")
	}
}
