package registrationguard

import (
	"reflect"
	"testing"
)

func TestIndependentEvidenceRequired(t *testing.T) {
	cases := []struct {
		name string
		e    Evidence
		want Decision
	}{
		{"no evidence", Evidence{}, Decision{}},
		{"campus shared network", Evidence{NetworkPeers: 300}, Decision{}},
		{"copied tutorial", Evidence{TemplatePeers: 30}, Decision{Alert: true}},
		{"reward reuse alone", Evidence{IdentityRewardUsed: true}, Decision{Alert: true, Hold: true}},
		{"campaign plus network", Evidence{TemplatePeers: 4, NetworkPeers: 3}, Decision{Alert: true, Hold: true}},
		{"independent conjunction", Evidence{TemplatePeers: 4, NetworkPeers: 3, IdentityRewardUsed: true}, Decision{Alert: true, Hold: true, CanSuspend: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Evaluate(tc.e); got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestFingerprintsIgnoreShortAnswersAndDoNotExposeText(t *testing.T) {
	for _, s := range []string{"hi", "我就是想用QQ邮箱注册", "名字随便起的", "嗯，先等等", "复制好了，谢谢"} {
		if _, tokens := Fingerprints("secret", s); len(tokens) != 0 {
			t.Fatalf("short answer indexed: %q", s)
		}
	}
	s := "I need help connecting my existing desktop application to this service and running a minimal request with the correct endpoint."
	a, tokens := Fingerprints("secret", s)
	b, other := Fingerprints("secret", s+"!!!")
	if a != b || !reflect.DeepEqual(tokens, other) || len(tokens) != 8 {
		t.Fatal("normalization or storage bound is incorrect")
	}
	if _, other = Fingerprints("different", s); reflect.DeepEqual(tokens, other) {
		t.Fatal("cross-installation fingerprints must differ")
	}
	for _, token := range tokens {
		if len(token) != 64 {
			t.Fatal("not a digest")
		}
	}
	if _, tokens = Fingerprints("", s); len(tokens) > 0 {
		t.Fatal("missing key must fail closed")
	}
}
