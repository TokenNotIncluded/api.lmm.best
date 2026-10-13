// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package assistant

import (
	"errors"
	"strings"
	"testing"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
)

func TestGreetingTemplateMatchesOverviewContract(t *testing.T) {
	for _, language := range greetingLanguages {
		for _, template := range []string{"", " \n\t", "$name $time $date $weekday $level $site $balance $$", "$$unknown $ $5", strings.Repeat("😀", 512), "<script>$name</script>"} {
			if !validGreetingTemplate(language, template) {
				t.Fatalf("valid template rejected for %s", language)
			}
		}
	}
	for _, tc := range [][2]string{{"zh-CN", "hello"}, {"", "hello"}, {"en", strings.Repeat("😀", 513)}, {"en", "\x00"}, {"en", string([]byte{0xff})}, {"en", "$unknown"}, {"en", "$name_suffix"}, {"en", "$$$unknown"}} {
		if validGreetingTemplate(tc[0], tc[1]) {
			t.Fatalf("invalid template accepted: %q", tc)
		}
	}
}

func TestGreetingMissingStorageDoesNotInventSuccessfulPreference(t *testing.T) {
	s, _, identity := setup(t, Options{})
	history, err := NewHistory(&historySpy{}, s.auth, []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = history.Greeting(ctx, user); !errors.Is(err, access.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err = history.UpdateGreeting(ctx, user, "en", "Hello", 0); !errors.Is(err, access.ErrUnavailable) {
		t.Fatal(err)
	}
	identity.Revoke(user)
	if _, err = history.Greeting(ctx, user); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatal(err)
	}
	if _, err = history.UpdateGreeting(ctx, user, "en", "Hello", 0); !errors.Is(err, access.ErrUnauthorized) {
		t.Fatal(err)
	}
}
