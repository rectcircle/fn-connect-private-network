package main

import (
	"bytes"
	"testing"
)

func TestFramesRejectTruncationAndOversize(t *testing.T) {
	for _, raw := range [][]byte{{0, 0}, {0, 0, 0, 0}, {0, 4, 0, 1}, {0, 0, 0, 3, 1}} {
		if _, err := readFrame(bytes.NewReader(raw)); err == nil {
			t.Fatal("accepted invalid frame")
		}
	}
}
func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := []byte(`{"version":1,"method":"status"}`)
	if err := writeFrame(&buf, want); err != nil {
		t.Fatal(err)
	}
	got, err := readFrame(&buf)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("frame mismatch", err)
	}
}
