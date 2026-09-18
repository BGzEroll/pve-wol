package wol

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

const magicPacketLength = 6 + 16*6

var magicPrefix = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

func ParseMagicPacket(payload []byte) (string, bool) {
	if len(payload) < magicPacketLength {
		return "", false
	}

	for offset := 0; offset <= len(payload)-magicPacketLength; offset++ {
		if !bytes.Equal(payload[offset:offset+len(magicPrefix)], magicPrefix) {
			continue
		}

		macStart := offset + len(magicPrefix)
		mac := payload[macStart : macStart+6]
		valid := true
		for repeat := 1; repeat < 16; repeat++ {
			start := macStart + repeat*6
			if !bytes.Equal(payload[start:start+6], mac) {
				valid = false
				break
			}
		}
		if valid {
			return formatMAC(mac), true
		}
	}
	return "", false
}

func formatMAC(mac []byte) string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
}

func Open(address string) (*net.UDPConn, error) {
	udpAddress, err := net.ResolveUDPAddr("udp4", address)
	if err != nil {
		return nil, err
	}
	return net.ListenUDP("udp4", udpAddress)
}

func Serve(ctx context.Context, conn *net.UDPConn, handler func(string)) error {
	defer conn.Close()
	buffer := make([]byte, 2048)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		n, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return err
		}
		if mac, ok := ParseMagicPacket(buffer[:n]); ok {
			handler(mac)
		}
	}
}

type Debouncer struct {
	mu       sync.Mutex
	interval time.Duration
	last     map[string]time.Time
}

func NewDebouncer(interval time.Duration) *Debouncer {
	return &Debouncer{interval: interval, last: make(map[string]time.Time)}
}

func (d *Debouncer) Allow(mac string) bool {
	if d.interval <= 0 {
		return true
	}

	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	for oldMAC, timestamp := range d.last {
		if now.Sub(timestamp) > d.interval*2 {
			delete(d.last, oldMAC)
		}
	}
	if previous, ok := d.last[mac]; ok && now.Sub(previous) < d.interval {
		return false
	}
	d.last[mac] = now
	return true
}
