package wol

import "testing"

func TestParseMagicPacket(t *testing.T) {
	mac := []byte{0xbc, 0x24, 0x11, 0xaa, 0xbb, 0xcc}
	payload := []byte{0x01, 0x02, 0x03}
	payload = append(payload, magicPrefix...)
	for i := 0; i < 16; i++ {
		payload = append(payload, mac...)
	}

	got, ok := ParseMagicPacket(payload)
	if !ok || got != "BC:24:11:AA:BB:CC" {
		t.Fatalf("ParseMagicPacket() = %q, %v", got, ok)
	}
}

func TestParseMagicPacketRejectsMismatchedRepeats(t *testing.T) {
	mac := []byte{0xbc, 0x24, 0x11, 0xaa, 0xbb, 0xcc}
	payload := append([]byte{}, magicPrefix...)
	for i := 0; i < 16; i++ {
		part := append([]byte{}, mac...)
		if i == 15 {
			part[5]++
		}
		payload = append(payload, part...)
	}

	if got, ok := ParseMagicPacket(payload); ok {
		t.Fatalf("ParseMagicPacket() accepted invalid packet as %q", got)
	}
}
