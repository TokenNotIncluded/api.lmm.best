package deploymentfence

import "testing"

func TestReservedOwnersIncludeNoncanonicalOptions(t *testing.T) {
	for _, key := range []string{OptionPrefix + "release:host", " merchantstoredeploymentfence:orphan ", "\tMERCHANTSTOREDEPLOYMENTFENCE:unknown\n"} {
		if !ReservedOptionKey(key) {
			t.Fatalf("durable owner key is writable: %q", key)
		}
	}
	for _, key := range []string{"MerchantStoreMinimumWriterCapability", "MerchantStoreDeploymentFence", "Other:" + OptionPrefix} {
		if ReservedOptionKey(key) {
			t.Fatalf("unrelated option was reserved: %q", key)
		}
	}
	if AdvisoryKey == 0x4c4d4d4150490001 {
		t.Fatal("deployment fence would deadlock provider VERIFY")
	}
}
