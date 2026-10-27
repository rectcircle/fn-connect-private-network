package main

import "encoding/binary"

func checksum(p []byte) uint16 {
	var sum uint32
	for len(p) > 1 {
		sum += uint32(binary.BigEndian.Uint16(p))
		p = p[2:]
	}
	if len(p) > 0 {
		sum += uint32(p[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	return ^uint16(sum)
}

// Echo only the diagnostic IPv4 UDP endpoint. No forwarding/default route.
func echoPacket(packet []byte) []byte {
	if len(packet) < 28 || packet[0] != 0x45 || packet[9] != 17 || packet[6]&0x3f != 0 || packet[7] != 0 {
		return nil
	}
	size := int(binary.BigEndian.Uint16(packet[2:4]))
	if size < 28 || size > len(packet) {
		return nil
	}
	packet = packet[:size]
	if packet[16] != 198 || packet[17] != 18 || packet[18] != 0 || packet[19] != 1 || binary.BigEndian.Uint16(packet[22:24]) != 53535 {
		return nil
	}
	udpSize := int(binary.BigEndian.Uint16(packet[24:26]))
	if udpSize != size-20 {
		return nil
	}
	reply := append([]byte(nil), packet...)
	copy(reply[12:16], packet[16:20])
	copy(reply[16:20], packet[12:16])
	copy(reply[20:22], packet[22:24])
	copy(reply[22:24], packet[20:22])
	reply[10] = 0
	reply[11] = 0
	binary.BigEndian.PutUint16(reply[10:12], checksum(reply[:20]))
	reply[26] = 0
	reply[27] = 0 // IPv4 UDP permits an omitted checksum.
	return reply
}
