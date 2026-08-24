package appcli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDispatchShouldServeWhenNoCommandIsProvided(t *testing.T) {
	result := Dispatch(nil, "1.2.3", io.Discard, io.Discard)
	if result.Mode != ModeServe {
		t.Fatalf("Dispatch() mode = %v, want ModeServe", result.Mode)
	}
}

func TestDispatchShouldStripServeCommandFromServerArguments(t *testing.T) {
	result := Dispatch([]string{"serve", "--port", "3100"}, "1.2.3", io.Discard, io.Discard)
	if result.Mode != ModeServe || strings.Join(result.ServeArgs, " ") != "--port 3100" {
		t.Fatalf("Dispatch() = %#v, want serve args --port 3100", result)
	}
}

func TestDispatchRequiresExplicitMigrationMode(t *testing.T) {
	for _, test := range []struct {
		argument string
		mode     Mode
	}{
		{argument: "--apply", mode: ModeMigrateApply},
		{argument: "--verify", mode: ModeMigrateVerify},
	} {
		t.Run(test.argument, func(t *testing.T) {
			result := Dispatch([]string{"migrate", test.argument}, "1.2.3", io.Discard, io.Discard)
			if result.Mode != test.mode || result.ExitCode != ExitOK {
				t.Fatalf("Dispatch() = %#v, want mode %v", result, test.mode)
			}
		})
	}

	for _, args := range [][]string{
		{"migrate"},
		{"migrate", "--apply", "--verify"},
		{"migrate", "--write"},
	} {
		var stderr bytes.Buffer
		result := Dispatch(args, "1.2.3", io.Discard, &stderr)
		if result.Mode != ModeExit || result.ExitCode != ExitUsage {
			t.Fatalf("Dispatch(%q) = %#v, want explicit usage failure", args, result)
		}
		if !strings.Contains(stderr.String(), "choose exactly one") {
			t.Fatalf("Dispatch(%q) stderr = %q", args, stderr.String())
		}
	}
}

func TestRequestShouldSendNativeHTTPAndPersistOutputs(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer secret-token" {
			t.Errorf("Authorization header was not populated from the token file")
		}
		if request.Header.Get("X-LMM-Test") != "native" {
			t.Errorf("X-LMM-Test = %q, want native", request.Header.Get("X-LMM-Test"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if string(body) != `{"persona":"buyer"}` {
			t.Errorf("body = %q, want buyer JSON", body)
		}
		http.SetCookie(writer, &http.Cookie{Name: "refresh", Value: "cookie-secret", Path: "/", HttpOnly: true})
		writer.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(writer, `{"success":true}`)
	}))
	defer server.Close()

	directory := t.TempDir()
	tokenFile := filepath.Join(directory, "token")
	cookieFile := filepath.Join(directory, "cookies.json")
	statusFile := filepath.Join(directory, "status")
	if err := os.WriteFile(tokenFile, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	result := Dispatch([]string{
		"request", "--url", server.URL + "/login", "--method", "POST", "--json",
		"--body", `{"persona":"buyer"}`, "--header", "X-LMM-Test: native",
		"--token-file", tokenFile, "--cookie-file", cookieFile, "--status-file", statusFile,
	}, "1.2.3", &stdout, &stderr)
	if result.ExitCode != ExitOK {
		t.Fatalf("request exit = %d, stderr = %q", result.ExitCode, stderr.String())
	}
	if stdout.String() != `{"success":true}` {
		t.Fatalf("stdout = %q, want success body", stdout.String())
	}
	status, err := os.ReadFile(statusFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(status) != "201\n" {
		t.Fatalf("status = %q, want 201", status)
	}
	cookies, err := os.ReadFile(cookieFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(cookies, []byte("cookie-secret")) || bytes.Contains(cookies, []byte("secret-token")) {
		t.Fatalf("cookie store did not preserve only the response cookie")
	}
	info, err := os.Stat(cookieFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cookie file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestRequestShouldReusePersistentCookies(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/login":
			http.SetCookie(writer, &http.Cookie{Name: "refresh", Value: "persisted", Path: "/", HttpOnly: true})
			writer.WriteHeader(http.StatusNoContent)
		case "/session":
			cookie, err := request.Cookie("refresh")
			if err != nil || cookie.Value != "persisted" {
				http.Error(writer, "missing cookie", http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(writer, "session-ok")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	cookieFile := filepath.Join(t.TempDir(), "cookies.json")
	if code := RunRequest([]string{"--url", server.URL + "/login", "--cookie-file", cookieFile, "--fail"}, "test", io.Discard, io.Discard); code != ExitOK {
		t.Fatalf("login exit = %d", code)
	}
	var body bytes.Buffer
	if code := RunRequest([]string{"--url", server.URL + "/session", "--cookie-file", cookieFile, "--fail"}, "test", &body, io.Discard); code != ExitOK {
		t.Fatalf("session exit = %d", code)
	}
	if body.String() != "session-ok" {
		t.Fatalf("body = %q, want session-ok", body.String())
	}
}

func TestRequestShouldReturnCurlCompatibleHTTPFailureAfterWritingBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(writer, `{"code":"LEGAL_CONSENT_REQUIRED"}`)
	}))
	defer server.Close()

	var body bytes.Buffer
	code := RunRequest([]string{"--url", server.URL, "--fail"}, "test", &body, io.Discard)
	if code != ExitHTTPFailure {
		t.Fatalf("exit = %d, want %d", code, ExitHTTPFailure)
	}
	if body.String() != `{"code":"LEGAL_CONSENT_REQUIRED"}` {
		t.Fatalf("body = %q, want error response", body.String())
	}
}

func TestRequestShouldNotForwardBearerAcrossOrigins(t *testing.T) {
	var leaked string
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		leaked = request.Header.Get("Authorization")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", target.URL)
		writer.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()

	t.Setenv("LMM_TEST_BEARER", "redirect-secret")
	code := RunRequest([]string{"--url", redirect.URL, "--token-env", "LMM_TEST_BEARER"}, "test", io.Discard, io.Discard)
	if code != ExitOK {
		t.Fatalf("exit = %d, want success", code)
	}
	if leaked != "" {
		t.Fatalf("Authorization leaked across origins: %q", leaked)
	}
}

func TestStatusShouldUseStatusRoute(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/status" {
			http.Error(writer, fmt.Sprintf("unexpected path %s", request.URL.Path), http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(writer, "status-ok")
	}))
	defer server.Close()

	var body bytes.Buffer
	result := Dispatch([]string{"status", "--base-url", server.URL}, "test", &body, io.Discard)
	if result.ExitCode != ExitOK || body.String() != "status-ok" {
		t.Fatalf("status result = %#v body = %q", result, body.String())
	}
}

func TestStatusShouldNotAllowRouteOrMethodOverride(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/status" || request.Method != http.MethodGet {
			http.Error(writer, "route alias was overridden", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(writer, "status-ok")
	}))
	defer server.Close()

	var body bytes.Buffer
	result := Dispatch([]string{
		"status", "--base-url", server.URL, "--path", "/api/livez", "--method", "POST",
	}, "test", &body, io.Discard)
	if result.ExitCode != ExitOK || body.String() != "status-ok" {
		t.Fatalf("status result = %#v body = %q", result, body.String())
	}
}

func TestUsageNamesTheCanonicalBackendBinary(t *testing.T) {
	var output bytes.Buffer
	WriteUsage(&output)
	if !strings.Contains(output.String(), "lmm-api request") || !strings.Contains(output.String(), "lmm-api deploy production apply") {
		t.Fatalf("usage does not name %s: %q", ProgramName, output.String())
	}
	if strings.Contains(output.String(), "lmm-api-go request") || strings.Contains(output.String(), "lmm-api-deploy") {
		t.Fatalf("usage exposes a second command-line entry point: %q", output.String())
	}
}

func TestRequestShouldRejectNonHTTPURLWithoutLeakingToken(t *testing.T) {
	var stderr bytes.Buffer
	t.Setenv("LMM_TEST_SECRET", "never-print-this")
	code := RunRequest([]string{"--url", "file:///etc/passwd", "--token-env", "LMM_TEST_SECRET"}, "test", io.Discard, &stderr)
	if code == ExitOK {
		t.Fatal("file URL was accepted")
	}
	if strings.Contains(stderr.String(), "never-print-this") {
		t.Fatal("token was printed in an error")
	}
}
