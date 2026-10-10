package main

import "testing"

func TestCreditPreparationListenerCannotExposeHealthRemotely(t *testing.T) {
	for _, bind := range []string{"0.0.0.0", "::", "192.0.2.1"} {
		if _, err := creditPreparationListenAddress(bind, "3000"); err == nil {
			t.Fatalf("accepted public preparation listener %s", bind)
		}
	}
	for _, bind := range []string{"", "127.0.0.1", "::1"} {
		if _, err := creditPreparationListenAddress(bind, "3000"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := creditPreparationListenAddress("127.0.0.1", "0"); err == nil {
		t.Fatal("accepted unbound port")
	}
}
