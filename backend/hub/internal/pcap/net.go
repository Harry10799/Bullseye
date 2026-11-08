package pcap

import "net"

// netParseIP wraps net.ParseIP so it can be swapped/replaced for testing.
func netParseIP(s string) []byte {
	ip := net.ParseIP(s)
	if ip == nil {
		return nil
	}
	// return 4-byte IPv4 representation when possible
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}
