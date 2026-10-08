package appcli

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestProductionDatabaseCommandPreservesPGXQueryEncoding(t *testing.T) {
	t.Setenv("PGOPTIONS", "-c application_name=ambient")
	tests := []struct {
		name       string
		parameters string
		options    *string
		appName    string
	}{
		{"form-options-only", "options=" + url.QueryEscape("-c search_path=fenced -c default_transaction_read_only=on"), stringPointer("-c search_path=fenced -c default_transaction_read_only=on"), ""},
		{"percent-options-only", "options=-c%20search_path%3Dfenced%20-c%20default_transaction_read_only%3Don", stringPointer("-c search_path=fenced -c default_transaction_read_only=on"), ""},
		{"literal-plus", "options=-c+application_name%3Da%2Bb", stringPointer("-c application_name=a+b"), ""},
		{"already-encoded-plus", "options=-c%20application_name%3Da%2Bb", stringPointer("-c application_name=a+b"), ""},
		{"encoded-percent", "options=-c+application_name%3Da%2520b%252Bc", stringPointer("-c application_name=a%20b%2Bc"), ""},
		{"empty-options", "options=", stringPointer(""), ""},
		{"bare-empty-options", "options", stringPointer(""), ""},
		{"missing-options", "", nil, ""},
		{"form-without-options", "application_name=form+name%2Bplus", nil, "form name+plus"},
		{"empty-options-with-form-value", "options=&application_name=form+name%2Bplus", stringPointer(""), "form name+plus"},
		{"unknown-preserved", "options=-c+search_path%3Dfenced&future_setting=private+value%2Bplus", stringPointer("-c search_path=fenced"), ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := "postgres://business@database.example/lmm?sslmode=require&" + test.parameters
			values := map[string]string{"SQL_DSN": original, "PGOPTIONS": "-c application_name=inspected"}
			adapted, environment, err := productionDatabaseCommand(values)
			if err != nil {
				t.Fatal(err)
			}
			originalConfig, err := pgx.ParseConfig(original)
			if err != nil {
				t.Fatal(err)
			}
			parsed := mustDatabaseURL(t, adapted)
			query := parsed.Query()
			if query.Get("sslmode") != "require" || parsed.User.Username() != "business" || parsed.Host != "database.example" || parsed.Path != "/lmm" {
				t.Fatal("normalization changed connection identity or SSL mode")
			}
			if query.Has("options") != (test.options != nil) || test.options != nil && query.Get("options") != *test.options {
				t.Fatalf("options=%q present=%t", query.Get("options"), query.Has("options"))
			}
			if query.Get("application_name") != test.appName {
				t.Fatalf("application_name=%q want=%q", query.Get("application_name"), test.appName)
			}
			// libpq decodes percent escapes without form decoding. Verify the
			// actual command URI, rather than Go's Query() decoding it again.
			for _, pair := range strings.Split(parsed.RawQuery, "&") {
				key, rawValue, _ := strings.Cut(pair, "=")
				value, err := url.PathUnescape(rawValue)
				if err != nil || strings.Contains(rawValue, "+") {
					t.Fatal("command URI still relies on form decoding")
				}
				if key == "options" || key == "application_name" || key == "future_setting" {
					if value != originalConfig.RuntimeParams[key] {
						t.Fatalf("libpq %s=%q differs from pgx %q", key, value, originalConfig.RuntimeParams[key])
					}
				}
			}
			if strings.Contains(test.parameters, "future_setting=") && query.Get("future_setting") != "private value+plus" {
				t.Fatal("unknown parameter was silently dropped or changed")
			}
			if values["SQL_DSN"] != original || values["PGOPTIONS"] != "-c application_name=inspected" ||
				!containsString(environment, "SQL_DSN="+original) || !containsString(environment, "PGOPTIONS=-c application_name=inspected") {
				t.Fatal("normalization changed the inspected pgx environment")
			}
		})
	}
}

func TestProductionDatabaseCommandPreservesUnconvertedParameterOrder(t *testing.T) {
	const originalQuery = "sslmode=disable&ssl=true&options=-c+search_path%3Dfenced&future_setting=first+value&future_setting=last%2Bvalue"
	adapted, _, err := productionDatabaseCommand(map[string]string{"SQL_DSN": "postgres://business@database.example/lmm?" + originalQuery})
	if err != nil {
		t.Fatal(err)
	}
	if mustDatabaseURL(t, adapted).RawQuery != "sslmode=disable&ssl=true&options=-c%20search_path%3Dfenced&future_setting=first%20value&future_setting=last%2Bvalue" {
		t.Fatal("normalization reordered aliases, dropped duplicate unknown parameters, or changed a literal plus")
	}
}

func TestProductionDatabaseCommandAdaptsKnownRuntimeParameters(t *testing.T) {
	t.Setenv("PGOPTIONS", "-c statement_timeout=ambient")
	tests := []struct {
		name       string
		parameters string
		pgOptions  *string
		want       string
	}{
		{"uri-options-win", "options=-c%20statement_timeout%3D25%20-c%20search_path%3Dother%20-c%20default_transaction_read_only%3Doff&search_path=fenced&default_transaction_read_only=on", stringPointer("-c statement_timeout=ignored"), "-c statement_timeout=25 -c search_path=other -c default_transaction_read_only=off -c search_path=fenced -c default_transaction_read_only=on"},
		{"form-uri-options-win", "options=-c+search_path%3Dother+-c+default_transaction_read_only%3Doff&search_path=fenced&default_transaction_read_only=on", stringPointer("-c statement_timeout=ignored"), "-c search_path=other -c default_transaction_read_only=off -c search_path=fenced -c default_transaction_read_only=on"},
		{"explicit-environment", "search_path=fenced", stringPointer("-c statement_timeout=50"), "-c statement_timeout=50 -c search_path=fenced"},
		{"ambient-environment", "search_path=fenced", nil, "-c statement_timeout=ambient -c search_path=fenced"},
		{"explicit-empty-environment", "search_path=fenced", stringPointer(""), "-c search_path=fenced"},
		{"explicit-empty-uri-options", "options=&search_path=fenced", stringPointer("-c statement_timeout=ignored"), "-c search_path=fenced"},
		{"empty-direct-value", "options=&search_path=", nil, "-c search_path="},
		{"empty-read-only-value", "options=&default_transaction_read_only=", nil, "-c default_transaction_read_only="},
		{"quoted-schema-and-backslash", "options=&search_path=%22space%20schema%22%2C%22slash%5Cname%22%2Cpublic", nil, `-c search_path="space\ schema","slash\\name",public`},
		{"whitespace-is-one-value", "options=&search_path=a%09b%0Ac%0Dd%0Be%0Cf%20g", nil, "-c search_path=a\\\tb\\\nc\\\rd\\\ve\\\ff\\ g"},
		{"literal-plus", "options=&search_path=a%2Bb", nil, "-c search_path=a+b"},
		{"unknown-parameter-preserved", "options=&search_path=fenced&future_setting=private-value", nil, "-c search_path=fenced"},
		{"complete-option-backslash", "options=-c%20application_name%3Dslash%5C%5C&search_path=fenced", nil, `-c application_name=slash\\ -c search_path=fenced`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := "postgresql://app:fixture%40password@database.example:55432/lmm?sslmode=require&host=%2Fprivate%2Fsocket&user=business&port=5433&" + test.parameters
			values := map[string]string{"SQL_DSN": original}
			if test.pgOptions != nil {
				values["PGOPTIONS"] = *test.pgOptions
			}
			adapted, environment, err := productionDatabaseCommand(values)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(adapted)
			if err != nil {
				t.Fatal(err)
			}
			query := parsed.Query()
			if query.Get("options") != test.want || query.Has("search_path") || query.Has("default_transaction_read_only") {
				t.Fatalf("libpq runtime parameters=%v want options=%q", query, test.want)
			}
			if parsed.Scheme != "postgresql" || parsed.Host != "database.example:55432" || parsed.Path != "/lmm" || parsed.User.Username() != "app" ||
				query.Get("sslmode") != "require" || query.Get("host") != "/private/socket" || query.Get("user") != "business" || query.Get("port") != "5433" {
				t.Fatal("libpq adaptation changed connection identity or SSL settings")
			}
			if strings.Contains(adapted, "fixture") || strings.Contains(adapted, "+") || !strings.Contains(adapted, "%20") || !containsString(environment, "PGPASSWORD=fixture@password") {
				t.Fatal("password escaped its environment or options were not URI encoded for libpq")
			}
			if values["SQL_DSN"] != original || !containsString(environment, "SQL_DSN="+original) {
				t.Fatal("libpq adaptation modified the original pgx connection")
			}
			if strings.Contains(test.parameters, "future_setting=") && query.Get("future_setting") != "private-value" {
				t.Fatal("unknown parameter was silently dropped")
			}
		})
	}
}

func TestProductionDatabaseCommandRejectsAmbiguousRuntimeParameters(t *testing.T) {
	for _, parameters := range []string{
		"search_path=private-value&search_path=other", "default_transaction_read_only=on&default_transaction_read_only=off",
		"options=private-value&options=other", "search_path=private-value%00", "default_transaction_read_only=on%00",
		"options=private-value%00&search_path=fenced", "options=private-value%5C&search_path=fenced", "search_path=%zz",
		"password=private-value&password=other", "password=private-value%00",
	} {
		t.Run(parameters, func(t *testing.T) {
			_, _, err := productionDatabaseCommand(map[string]string{"SQL_DSN": "postgres://app:secret@localhost/lmm?" + parameters})
			if err == nil {
				t.Fatal("ambiguous or invalid connection parameters accepted")
			}
			if strings.Contains(err.Error(), "private-value") || strings.Contains(err.Error(), "secret") {
				t.Fatal("private connection values leaked in adapter error")
			}
		})
	}
	for _, options := range []string{"private-value\x00", `private-value\`} {
		if _, _, err := productionDatabaseCommand(map[string]string{"SQL_DSN": "postgres://localhost/lmm?search_path=fenced", "PGOPTIONS": options}); err == nil {
			t.Fatal("unsafe environment options accepted")
		}
	}
}

func TestProductionDatabaseCommandKeepsQueryPasswordOutOfArguments(t *testing.T) {
	for _, test := range []struct{ encoded, decoded string }{
		{"query%40password", "query@password"},
		{"query+password%2Bplus%2520", "query password+plus%20"},
		{"query%20password%2Bplus%2520", "query password+plus%20"},
		{"", ""},
	} {
		t.Run(test.encoded, func(t *testing.T) {
			original := "postgres://app:original@localhost/lmm?password=" + test.encoded + "&sslmode=require"
			values := map[string]string{"SQL_DSN": original}
			adapted, environment, err := productionDatabaseCommand(values)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(adapted, "password") || strings.Contains(adapted, "original") || !containsString(environment, "PGPASSWORD="+test.decoded) {
				t.Fatal("URI query password did not override userinfo privately")
			}
			if values["SQL_DSN"] != original || !containsString(environment, "SQL_DSN="+original) {
				t.Fatal("password extraction changed the inspected pgx environment")
			}
		})
	}
}

func TestExistingSchemaEffectiveSearchPathDoesNotPromoteAmbientOptions(t *testing.T) {
	t.Setenv("PGOPTIONS", "-c search_path=other -c statement_timeout=123")
	values := map[string]string{"SQL_DSN": "postgres://localhost/lmm?search_path=public"}
	calls := 0
	runtime := productionRuntime{runner: existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		calls++
		parsed, err := url.Parse(command.Args[len(command.Args)-1])
		if err != nil || parsed.Query().Get("options") != "-c search_path=public" || !containsString(command.Env, "PGOPTIONS=") {
			t.Fatal("operator ambient options reached inspected process identity proof")
		}
		return testExistingSchemaIdentitySnapshot(), nil
	}}}
	if err := runtime.verifyExistingSchemaEffectiveSearchPath(context.Background(), values, testExistingSchemaContract(t)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("process identity proof was not executed")
	}
	if _, present := values["PGOPTIONS"]; present {
		t.Fatal("process identity proof changed inspected environment values")
	}
}

func TestProductionSealedDatabaseCommandKeepsOnlyInspectedEnvironment(t *testing.T) {
	t.Setenv("PGHOST", "uninspected-host")
	t.Setenv("PGPASSWORD", "uninspected-password")
	t.Setenv("PGOPTIONS", "-c search_path=uninspected")
	values := map[string]string{
		"SQL_DSN": "postgres://business@database.example/lmm?password=private-query-password&search_path=fenced&default_transaction_read_only=on",
		"PATH":    "/usr/bin:/bin",
	}
	adapted, environment, err := productionSealedDatabaseCommand(values)
	if err != nil {
		t.Fatal(err)
	}
	if urlValues := mustDatabaseURL(t, adapted).Query(); urlValues.Get("options") != "-c search_path=fenced -c default_transaction_read_only=on" || urlValues.Has("password") {
		t.Fatal("sealed libpq configuration lost the guards or retained its password")
	}
	if !containsString(environment, "PGPASSWORD=private-query-password") || !containsString(environment, "PGOPTIONS=") || !containsString(environment, "PATH=/usr/bin:/bin") {
		t.Fatal("sealed command lost the private URI password or inspected environment")
	}
	for _, assignment := range environment {
		key, _, _ := strings.Cut(assignment, "=")
		if key != "SQL_DSN" && key != "PATH" && key != "PGOPTIONS" && key != "PGPASSWORD" {
			t.Fatal("uninspected operator environment reached sealed database command")
		}
	}
	if _, present := values["PGOPTIONS"]; present {
		t.Fatal("sealed command modified inspected environment")
	}
}

func TestProductionMerchantStoreCapsuleSchemaUsesSealedLibpqCommand(t *testing.T) {
	t.Setenv("PGHOST", "uninspected-host")
	t.Setenv("PGPASSWORD", "uninspected-password")
	contract := testExistingSchemaContract(t)
	capsule := productionMerchantStoreCapsule{Writer: &productionMerchantStoreWriterContract{Schema: "public"}, ExistingSchemaContract: contract}
	calls := 0
	runtime := productionRuntime{runner: existingSchemaTestRunner{run: func(command productionCommand) ([]byte, error) {
		calls++
		if command.Name != commandPSQL || !command.Sensitive {
			t.Fatal("capsule verification did not use its sensitive psql boundary")
		}
		dsn := command.Args[len(command.Args)-1]
		query := mustDatabaseURL(t, dsn).Query()
		if query.Has("search_path") || query.Has("default_transaction_read_only") || query.Has("password") || strings.Contains(dsn, "private-query-password") ||
			query.Get("options") != "-csearch_path=ignored -cdefault_transaction_read_only=off -c search_path=public -c default_transaction_read_only=on" {
			t.Fatal("capsule passed a pgx-only parameter, password, or an unfenced configuration to libpq")
		}
		if !containsString(command.Env, "PGPASSWORD=private-query-password") || containsString(command.Env, "PGHOST=uninspected-host") || containsString(command.Env, "PGPASSWORD=uninspected-password") {
			t.Fatal("capsule did not retain its private connection authority")
		}
		return testExistingSchemaSnapshot(), nil
	}}}
	child := []string{"SQL_DSN=postgres://business@localhost/lmm?options=-csearch_path%3Dignored%20-cdefault_transaction_read_only%3Doff&search_path=public&default_transaction_read_only=on&password=private-query-password", "PGOPTIONS=-c search_path=other", "PATH=/usr/bin:/bin"}
	if err := runtime.verifyMerchantStoreCapsuleSchema(context.Background(), capsule, child); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("capsule did not execute its schema verification")
	}
}

func TestProductionMerchantStartupDatabaseRejectsAmbiguousRuntimeParameters(t *testing.T) {
	for _, assignment := range os.Environ() {
		key, _, _ := strings.Cut(assignment, "=")
		if strings.HasPrefix(key, "PG") {
			t.Setenv(key, "")
		}
	}
	for _, query := range []string{"options=private-value&options=other", "search_path=private-value&search_path=other", "default_transaction_read_only=on&default_transaction_read_only=off", "options=private-value%00"} {
		_, err := captureMerchantStartupDatabase(context.Background(), map[string]string{"SQL_DSN": "postgres://business@localhost/lmm?" + query})
		if err == nil || err.Error() != "production database runtime parameters are invalid" {
			t.Fatalf("ambiguous configuration was not rejected before connecting: %v", err)
		}
	}
}

func mustDatabaseURL(t *testing.T, dsn string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func stringPointer(value string) *string { return &value }
