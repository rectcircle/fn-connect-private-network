package main

import (
	"encoding/binary"
	"testing"
)

func probePacket() []byte {
	p := make([]byte, 32)
	p[0] = 0x45
	p[8] = 64
	p[9] = 17
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	copy(p[12:16], []byte{198, 18, 0, 2})
	copy(p[16:20], []byte{198, 18, 0, 1})
	binary.BigEndian.PutUint16(p[20:22], 12345)
	binary.BigEndian.PutUint16(p[22:24], 53535)
	binary.BigEndian.PutUint16(p[24:26], 12)
	copy(p[28:], []byte("test"))
	return p
}
func TestDiagnosticEchoPacket(t *testing.T) {
	p := probePacket()
	r := echoPacket(p)
	if len(r) != 32 || string(r[28:]) != "test" || checksum(r[:20]) != 0 {
		t.Fatal("invalid reply")
	}
	if binary.BigEndian.Uint16(r[20:22]) != 53535 || r[15] != 1 || r[19] != 2 {
		t.Fatal("endpoints not swapped")
	}
	if p[15] != 2 {
		t.Fatal("input mutated")
	}
}
func TestRejectUnrelatedAndMalformed(t *testing.T) {
	for _, mutate := range []func([]byte) []byte{
		func(p []byte) []byte { return p[:10] },
		func(p []byte) []byte { p[16] = 10; return p },
		func(p []byte) []byte { p[22] = 0; return p },
		func(p []byte) []byte { p[6] = 0x20; return p },
		func(p []byte) []byte { p[7] = 1; return p },
		func(p []byte) []byte { p[24] = 5; return p },
	} {
		if echoPacket(mutate(probePacket())) != nil {
			t.Fatal("unexpected forwarding")
		}
	}
}
