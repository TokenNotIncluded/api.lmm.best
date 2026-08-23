package appcli

import "testing"

func TestActionableJournalLineFiltersExpectedProxyDisconnects(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		actionable bool
	}{
		{
			name:       "nginx proxy disconnect during restart",
			line:       `nginx: [error] sendfile() failed (32: Broken pipe) while sending request to upstream`,
			actionable: false,
		},
		{
			name:       "unrelated broken pipe",
			line:       `backend: write failed (32: Broken pipe)`,
			actionable: true,
		},
		{
			name:       "local Go listener refusal during restart",
			line:       `2026/08/15 07:59:35 [error] 621262#621262: *605778 connect() failed (111: Connection refused) while connecting to upstream, client: 211.72.214.21, server: api.lmm.best, request: "GET /api/status HTTP/2.0", upstream: "http://127.0.0.1:3000/api/status", host: "api.lmm.best"`,
			actionable: false,
		},
		{
			name:       "unrelated upstream refusal",
			line:       `nginx: connect() failed (111: Connection refused) while connecting to upstream, upstream: "http://127.0.0.1:9000/health"`,
			actionable: true,
		},
		{
			name:       "client closes proxied response",
			line:       `nginx: [error] upstream prematurely closed connection while reading upstream, client: 203.0.113.10`,
			actionable: false,
		},
		{
			name:       "successful nginx reload notice",
			line:       `2026/08/23 05:58:15 [notice] 2472973#2472973: signal process started`,
			actionable: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := actionableJournalLine(test.line); got != test.actionable {
				t.Fatalf("actionableJournalLine()=%v, want %v", got, test.actionable)
			}
		})
	}
}
