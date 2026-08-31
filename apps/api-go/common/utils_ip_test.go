package common

import (
	"net"
	"testing"
)

func TestIsPrivateIPv4UsesRFC1918Boundaries(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{name: "ten network start", ip: "10.0.0.0", want: true},
		{name: "ten network end", ip: "10.255.255.255", want: true},
		{name: "carrier grade NAT", ip: "100.64.0.1", want: false},
		{name: "before 172 private range", ip: "172.15.255.255", want: false},
		{name: "172 private range start", ip: "172.16.0.0", want: true},
		{name: "172 private range end", ip: "172.31.255.255", want: true},
		{name: "after 172 private range", ip: "172.32.0.0", want: false},
		{name: "192 private range", ip: "192.168.1.1", want: true},
		{name: "documentation address", ip: "192.0.2.1", want: false},
		{name: "IPv6 unique local", ip: "fd00::1", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isPrivateIPv4(net.ParseIP(test.ip)); got != test.want {
				t.Fatalf("isPrivateIPv4(%q) = %t, want %t", test.ip, got, test.want)
			}
		})
	}
}
