package oauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

type gitHubEmailTransportFunc func(*http.Request) (*http.Response, error)

func (f gitHubEmailTransportFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func mockGitHubEmailTransport(t *testing.T, transport gitHubEmailTransportFunc) {
	t.Helper()
	previousTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
}

func gitHubEmailResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func setGitHubRegistrationEmailVerification(t *testing.T, enabled bool) {
	t.Helper()
	previous := common.EmailVerificationEnabled
	common.EmailVerificationEnabled = enabled
	t.Cleanup(func() { common.EmailVerificationEnabled = previous })
}

func requireGitHubEmailRequest(t *testing.T, req *http.Request) {
	t.Helper()
	require.Equal(t, http.MethodGet, req.Method)
	require.Equal(t, "https", req.URL.Scheme)
	require.Equal(t, "api.github.com", req.URL.Host)
	require.Equal(t, "Bearer test-token", req.Header.Get("Authorization"))
	if req.URL.Path == "/user/emails" {
		require.Equal(t, "application/vnd.github+json", req.Header.Get("Accept"))
		require.Equal(t, strconv.Itoa(gitHubEmailPageSize), req.URL.Query().Get("per_page"))
	}
}

func TestGitHubUserInfoCollectsAllVerifiedEmailsRegardlessOfRegistrationSetting(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("email_verification_enabled=%t", enabled), func(t *testing.T) {
			setGitHubRegistrationEmailVerification(t, enabled)
			var paths []string
			mockGitHubEmailTransport(t, func(req *http.Request) (*http.Response, error) {
				requireGitHubEmailRequest(t, req)
				paths = append(paths, req.URL.Path)
				switch req.URL.Path {
				case "/user":
					return gitHubEmailResponse(http.StatusOK, `{"id":42,"login":"legacy-owner","email":"public@example.com"}`), nil
				case "/user/emails":
					require.Equal(t, "1", req.URL.Query().Get("page"))
					return gitHubEmailResponse(http.StatusOK, `[
						{"email":"secondary@example.com","primary":false,"verified":true},
						{"email":"unverified@example.com","primary":true,"verified":false},
						{"email":"  ","primary":false,"verified":true},
						{"email":" primary@example.com ","primary":true,"verified":true},
						{"email":"other@example.com","primary":false,"verified":true}
					]`), nil
				default:
					t.Fatalf("unexpected GitHub request: %s", req.URL)
					return nil, errors.New("unexpected request")
				}
			})

			user, err := (&GitHubProvider{}).GetUserInfo(context.Background(), &OAuthToken{AccessToken: "test-token"})
			require.NoError(t, err)
			require.Equal(t, []string{"/user", "/user/emails"}, paths)
			require.Equal(t, "42", user.ProviderUserID)
			require.Equal(t, "legacy-owner", user.Extra["legacy_id"])
			require.Equal(t, "primary@example.com", user.Email)
			require.True(t, user.EmailVerified)
			require.Equal(t, []string{"primary@example.com", "secondary@example.com", "other@example.com"}, user.VerifiedEmails)
		})
	}
}

func TestGitHubUserInfoUsesVerifiedSecondaryWhenPrimaryIsUnverified(t *testing.T) {
	mockGitHubEmailTransport(t, func(req *http.Request) (*http.Response, error) {
		requireGitHubEmailRequest(t, req)
		if req.URL.Path == "/user" {
			return gitHubEmailResponse(http.StatusOK, `{"id":42,"login":"legacy-owner","email":"unverified@example.com"}`), nil
		}
		require.Equal(t, "/user/emails", req.URL.Path)
		return gitHubEmailResponse(http.StatusOK, `[
			{"email":"unverified@example.com","primary":true,"verified":false},
			{"email":"secondary@example.com","primary":false,"verified":true}
		]`), nil
	})

	user, err := (&GitHubProvider{}).GetUserInfo(context.Background(), &OAuthToken{AccessToken: "test-token"})
	require.NoError(t, err)
	require.Equal(t, "secondary@example.com", user.Email)
	require.True(t, user.EmailVerified)
	require.Equal(t, []string{"secondary@example.com"}, user.VerifiedEmails)
}

func TestGitHubEmailLookupFailureDoesNotVerifyPublicProfileEmail(t *testing.T) {
	tooManyEmails := strings.Repeat(`{"email":"verified@example.com","verified":true},`, gitHubEmailPageSize)
	tests := []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{name: "forbidden", status: http.StatusForbidden, body: `[{"email":"public@example.com","verified":true}]`},
		{name: "malformed", status: http.StatusOK, body: `[{"email":"public@example.com","verified":true},`},
		{name: "oversized", status: http.StatusOK, body: `[{"email":"` + strings.Repeat("x", int(oauthResponseBodyMaxBytes)) + `","verified":true}]`},
		{name: "too_many_addresses", status: http.StatusOK, body: `[` + tooManyEmails + `{"email":"extra@example.com","verified":true}]`},
		{name: "network", err: errors.New("GitHub unavailable")},
		{name: "no_verified_addresses", status: http.StatusOK, body: `[{"email":"public@example.com","primary":true,"verified":false},{"email":" ","verified":true}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setGitHubRegistrationEmailVerification(t, false)
			emailRequested := false
			mockGitHubEmailTransport(t, func(req *http.Request) (*http.Response, error) {
				requireGitHubEmailRequest(t, req)
				if req.URL.Path == "/user" {
					return gitHubEmailResponse(http.StatusOK, `{"id":42,"login":"legacy-owner","email":"public@example.com"}`), nil
				}
				require.Equal(t, "/user/emails", req.URL.Path)
				emailRequested = true
				if test.err != nil {
					return nil, test.err
				}
				return gitHubEmailResponse(test.status, test.body), nil
			})

			user, err := (&GitHubProvider{}).GetUserInfo(context.Background(), &OAuthToken{AccessToken: "test-token"})
			require.NoError(t, err)
			require.True(t, emailRequested)
			require.Equal(t, "public@example.com", user.Email)
			require.False(t, user.EmailVerified)
			require.Empty(t, user.VerifiedEmails)
		})
	}
}

type gitHubTrackedBody struct {
	io.Reader
	closed bool
}

func (body *gitHubTrackedBody) Close() error {
	body.closed = true
	return nil
}

func TestGitHubVerifiedEmailsIncludesLaterPagesWithoutFollowingLinkURLs(t *testing.T) {
	var pages []string
	var bodies []*gitHubTrackedBody
	mockGitHubEmailTransport(t, func(req *http.Request) (*http.Response, error) {
		requireGitHubEmailRequest(t, req)
		require.Equal(t, "/user/emails", req.URL.Path)
		page := req.URL.Query().Get("page")
		pages = append(pages, page)
		response := gitHubEmailResponse(http.StatusOK, "")
		var payload string
		switch page {
		case "1":
			payload = `[{"email":"secondary@example.com","verified":true}]`
			// Only pagination presence is trusted; the next request stays on GitHub.
			response.Header.Set("Link", `<https://outside.example/user/emails?page=999>; rel="next"`)
		case "2":
			payload = `[{"email":"primary@example.com","primary":true,"verified":true}]`
		default:
			t.Fatalf("unexpected page: %s", page)
		}
		body := &gitHubTrackedBody{Reader: strings.NewReader(payload)}
		bodies = append(bodies, body)
		response.Body = body
		return response, nil
	})

	emails := (&GitHubProvider{}).fetchVerifiedEmails(context.Background(), &OAuthToken{AccessToken: "test-token"})
	require.Equal(t, []string{"1", "2"}, pages)
	require.Equal(t, []string{"primary@example.com", "secondary@example.com"}, emails)
	for _, body := range bodies {
		require.True(t, body.closed)
	}
}

func TestGitHubVerifiedEmailsDiscardsIncompletePages(t *testing.T) {
	for _, test := range []struct {
		name          string
		failAfterPage int
	}{
		{name: "later_page_failure", failAfterPage: 1},
		{name: "pagination_limit", failAfterPage: gitHubEmailMaxPages},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			mockGitHubEmailTransport(t, func(req *http.Request) (*http.Response, error) {
				requireGitHubEmailRequest(t, req)
				require.Equal(t, "/user/emails", req.URL.Path)
				requests++
				require.Equal(t, strconv.Itoa(requests), req.URL.Query().Get("page"))
				if requests > test.failAfterPage {
					return gitHubEmailResponse(http.StatusForbidden, ""), nil
				}
				response := gitHubEmailResponse(http.StatusOK, `[{"email":"verified@example.com","verified":true}]`)
				response.Header.Set("Link", `<https://api.github.com/user/emails?page=2>; rel="next"`)
				return response, nil
			})

			emails := (&GitHubProvider{}).fetchVerifiedEmails(context.Background(), &OAuthToken{AccessToken: "test-token"})
			require.Empty(t, emails)
			if test.name == "later_page_failure" {
				require.Equal(t, 2, requests)
			} else {
				require.Equal(t, gitHubEmailMaxPages, requests)
			}
		})
	}
}

func TestGitHubVerifiedEmailsRejectsRedirects(t *testing.T) {
	for _, target := range []string{
		"https://outside.example/emails",
		"http://api.github.com/user/emails",
	} {
		t.Run(target, func(t *testing.T) {
			requests := 0
			body := &gitHubTrackedBody{Reader: strings.NewReader("")}
			mockGitHubEmailTransport(t, func(req *http.Request) (*http.Response, error) {
				requests++
				if requests > 1 {
					return gitHubEmailResponse(http.StatusOK, `[{"email":"attacker@example.com","verified":true}]`), nil
				}
				requireGitHubEmailRequest(t, req)
				response := gitHubEmailResponse(http.StatusFound, "")
				response.Header.Set("Location", target)
				response.Body = body
				return response, nil
			})

			emails := (&GitHubProvider{}).fetchVerifiedEmails(context.Background(), &OAuthToken{AccessToken: "test-token"})
			require.Empty(t, emails)
			require.Equal(t, 1, requests)
			require.True(t, body.closed)
		})
	}
}
