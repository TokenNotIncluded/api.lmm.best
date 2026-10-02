package main

import "testing"

func TestTestMCPListenRequiresTLSOutsideLoopback(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8123", ":8123", "example.com:8123", "192.168.1.2:8123", "not-an-address"} {
		if err := validateListen(address, "", ""); err == nil {
			t.Fatalf("plain HTTP must reject %q", address)
		}
	}
	for _, address := range []string{"127.0.0.1:8123", "[::1]:8123"} {
		if err := validateListen(address, "", ""); err != nil {
			t.Fatalf("loopback %q: %v", address, err)
		}
	}
	if err := validateListen(":443", "certificate.pem", "key.pem"); err != nil {
		t.Fatal(err)
	}
	if err := validateListen("127.0.0.1:8123", "certificate.pem", ""); err == nil {
		t.Fatal("missing TLS key must fail")
	}
}
