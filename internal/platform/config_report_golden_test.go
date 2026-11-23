package platform

import (
	"regexp"
	"testing"
)

// #207: the startup report is pinned byte for byte, so moving the effective
// values onto the registry rows cannot change a key, its order, a value's JSON
// type or the redaction of a Secret row (PC-06). #258 appends the git Source
// settings and GOLDIE_POLL_INTERVAL after the existing rows, so the #207 bytes
// stayed a prefix of every line until #398.
// #398 registers GOLDIE_ACTIVITY_PEERS and GOLDIE_ACTIVITY_REORDER_WINDOW
// after GOLDIE_MIN_CLIENT_VERSION, so every line gains them there, mid-line,
// and the #207 bytes are no longer a prefix (approved by the loop: the
// registry contract requires it). The last case pins them as written.

// reportTS matches the timestamp of a log line, the one part that varies.
var reportTS = regexp.MustCompile(`"ts":"[^"]*",`)

func TestStartupReportGolden(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "defaults",
			env:  nil,
			want: `{"level":"info","msg":"config","component":"server","version":"` + Version + `","GOLDIE_ADDR":":8080","GOLDIE_ADMIN_ADDR":":8081","GOLDIE_LOG_LEVEL":"info","GOLDIE_DRAIN_SECONDS":25,"GOLDIE_MODE":"all","GOLDIE_SWAGGER_UI_ENABLED":true,"GOLDIE_CONTENT_DIR":"","GOLDIE_API_TOKEN":"(unset)","GOLDIE_API_TOKENS":"(unset)","GOLDIE_SESSION_KEY":"(unset)","GOLDIE_RATE_LIMIT":"20/s,burst=40","GOLDIE_MIN_CLIENT_VERSION":"","GOLDIE_ACTIVITY_PEERS":"","GOLDIE_ACTIVITY_REORDER_WINDOW":"2s","GOLDIE_TELEMETRY_CLIENT_INFO":false,"GOLDIE_GIT_URL":"","GOLDIE_GIT_REF":"","GOLDIE_GIT_TOKEN":"(unset)","GOLDIE_GIT_SSH_KEY":"(unset)","GOLDIE_GIT_SSH_KNOWN_HOSTS":"(unset)","GOLDIE_GIT_CA_BUNDLE":"(unset)","GOLDIE_GIT_ALLOW_FILE_URL":false,"GOLDIE_SOURCES":"","GOLDIE_POLL_INTERVAL":"1m","GOLDIE_MCP_NAME":"goldie","GOLDIE_GITHUB_CLIENT_ID":"","GOLDIE_GITHUB_CLIENT_SECRET":"(unset)","GOLDIE_GITHUB_URL":"","GOLDIE_GITHUB_ORGS":"","GOLDIE_GITHUB_OPERATOR_TEAMS":"","GOLDIE_PUBLIC_URL":""}` + "\n",
		},
		{
			name: "every setting set",
			env: map[string]string{
				EnvAddr:                "127.0.0.1:9000",
				EnvAdminAddr:           "127.0.0.1:9001",
				EnvLogLevel:            "DEBUG",
				EnvDrainSeconds:        "7",
				EnvSwaggerUIEnabled:    "False",
				EnvContentDir:          "/srv/content",
				EnvAPIToken:            "golden-token-0123456789abcdef0123456789",
				EnvRateLimit:           "5/m",
				EnvMinClientVersion:    "1.4.0-rc.2",
				EnvTelemetryClientInfo: "true",
				envGitAllowFileURL:     "true",
				envPollInterval:        "90s",
			},
			want: `{"level":"info","msg":"config","component":"server","version":"` + Version + `","GOLDIE_ADDR":"127.0.0.1:9000","GOLDIE_ADMIN_ADDR":"127.0.0.1:9001","GOLDIE_LOG_LEVEL":"debug","GOLDIE_DRAIN_SECONDS":7,"GOLDIE_MODE":"all","GOLDIE_SWAGGER_UI_ENABLED":false,"GOLDIE_CONTENT_DIR":"/srv/content","GOLDIE_API_TOKEN":"(redacted)","GOLDIE_API_TOKENS":"(unset)","GOLDIE_SESSION_KEY":"(unset)","GOLDIE_RATE_LIMIT":"5/m","GOLDIE_MIN_CLIENT_VERSION":"1.4.0-rc.2","GOLDIE_ACTIVITY_PEERS":"","GOLDIE_ACTIVITY_REORDER_WINDOW":"2s","GOLDIE_TELEMETRY_CLIENT_INFO":true,"GOLDIE_GIT_URL":"","GOLDIE_GIT_REF":"","GOLDIE_GIT_TOKEN":"(unset)","GOLDIE_GIT_SSH_KEY":"(unset)","GOLDIE_GIT_SSH_KNOWN_HOSTS":"(unset)","GOLDIE_GIT_CA_BUNDLE":"(unset)","GOLDIE_GIT_ALLOW_FILE_URL":true,"GOLDIE_SOURCES":"","GOLDIE_POLL_INTERVAL":"90s","GOLDIE_MCP_NAME":"goldie","GOLDIE_GITHUB_CLIENT_ID":"","GOLDIE_GITHUB_CLIENT_SECRET":"(unset)","GOLDIE_GITHUB_URL":"","GOLDIE_GITHUB_ORGS":"","GOLDIE_GITHUB_OPERATOR_TEAMS":"","GOLDIE_PUBLIC_URL":""}` + "\n",
		},
		{
			name: "named tokens",
			env:  map[string]string{EnvAPITokens: "ci:reader:golden-token-0123456789abcdef0123456789"},
			want: `{"level":"info","msg":"config","component":"server","version":"` + Version + `","GOLDIE_ADDR":":8080","GOLDIE_ADMIN_ADDR":":8081","GOLDIE_LOG_LEVEL":"info","GOLDIE_DRAIN_SECONDS":25,"GOLDIE_MODE":"all","GOLDIE_SWAGGER_UI_ENABLED":true,"GOLDIE_CONTENT_DIR":"","GOLDIE_API_TOKEN":"(unset)","GOLDIE_API_TOKENS":"(redacted)","GOLDIE_SESSION_KEY":"(unset)","GOLDIE_RATE_LIMIT":"20/s,burst=40","GOLDIE_MIN_CLIENT_VERSION":"","GOLDIE_ACTIVITY_PEERS":"","GOLDIE_ACTIVITY_REORDER_WINDOW":"2s","GOLDIE_TELEMETRY_CLIENT_INFO":false,"GOLDIE_GIT_URL":"","GOLDIE_GIT_REF":"","GOLDIE_GIT_TOKEN":"(unset)","GOLDIE_GIT_SSH_KEY":"(unset)","GOLDIE_GIT_SSH_KNOWN_HOSTS":"(unset)","GOLDIE_GIT_CA_BUNDLE":"(unset)","GOLDIE_GIT_ALLOW_FILE_URL":false,"GOLDIE_SOURCES":"","GOLDIE_POLL_INTERVAL":"1m","GOLDIE_MCP_NAME":"goldie","GOLDIE_GITHUB_CLIENT_ID":"","GOLDIE_GITHUB_CLIENT_SECRET":"(unset)","GOLDIE_GITHUB_URL":"","GOLDIE_GITHUB_ORGS":"","GOLDIE_GITHUB_OPERATOR_TEAMS":"","GOLDIE_PUBLIC_URL":""}` + "\n",
		},
		{
			name: "git source",
			env: map[string]string{
				envGitURL:       gitSampleURL,
				envGitRef:       "main",
				envGitToken:     "glpat-golden-token-0123456789",
				envPollInterval: "10s",
			},
			want: `{"level":"info","msg":"config","component":"server","version":"` + Version + `","GOLDIE_ADDR":":8080","GOLDIE_ADMIN_ADDR":":8081","GOLDIE_LOG_LEVEL":"info","GOLDIE_DRAIN_SECONDS":25,"GOLDIE_MODE":"all","GOLDIE_SWAGGER_UI_ENABLED":true,"GOLDIE_CONTENT_DIR":"","GOLDIE_API_TOKEN":"(unset)","GOLDIE_API_TOKENS":"(unset)","GOLDIE_SESSION_KEY":"(unset)","GOLDIE_RATE_LIMIT":"20/s,burst=40","GOLDIE_MIN_CLIENT_VERSION":"","GOLDIE_ACTIVITY_PEERS":"","GOLDIE_ACTIVITY_REORDER_WINDOW":"2s","GOLDIE_TELEMETRY_CLIENT_INFO":false,"GOLDIE_GIT_URL":"https://git.example.com/org/content.git","GOLDIE_GIT_REF":"main","GOLDIE_GIT_TOKEN":"(redacted)","GOLDIE_GIT_SSH_KEY":"(unset)","GOLDIE_GIT_SSH_KNOWN_HOSTS":"(unset)","GOLDIE_GIT_CA_BUNDLE":"(unset)","GOLDIE_GIT_ALLOW_FILE_URL":false,"GOLDIE_SOURCES":"","GOLDIE_POLL_INTERVAL":"10s","GOLDIE_MCP_NAME":"goldie","GOLDIE_GITHUB_CLIENT_ID":"","GOLDIE_GITHUB_CLIENT_SECRET":"(unset)","GOLDIE_GITHUB_URL":"","GOLDIE_GITHUB_ORGS":"","GOLDIE_GITHUB_OPERATOR_TEAMS":"","GOLDIE_PUBLIC_URL":""}` + "\n",
		},
		{
			name: "activity peers",
			env: map[string]string{
				envActivityPeers:         "goldie-headless.goldie.svc",
				envActivityReorderWindow: "1m0s",
			},
			want: `{"level":"info","msg":"config","component":"server","version":"` + Version + `","GOLDIE_ADDR":":8080","GOLDIE_ADMIN_ADDR":":8081","GOLDIE_LOG_LEVEL":"info","GOLDIE_DRAIN_SECONDS":25,"GOLDIE_MODE":"all","GOLDIE_SWAGGER_UI_ENABLED":true,"GOLDIE_CONTENT_DIR":"","GOLDIE_API_TOKEN":"(unset)","GOLDIE_API_TOKENS":"(unset)","GOLDIE_SESSION_KEY":"(unset)","GOLDIE_RATE_LIMIT":"20/s,burst=40","GOLDIE_MIN_CLIENT_VERSION":"","GOLDIE_ACTIVITY_PEERS":"goldie-headless.goldie.svc","GOLDIE_ACTIVITY_REORDER_WINDOW":"1m0s","GOLDIE_TELEMETRY_CLIENT_INFO":false,"GOLDIE_GIT_URL":"","GOLDIE_GIT_REF":"","GOLDIE_GIT_TOKEN":"(unset)","GOLDIE_GIT_SSH_KEY":"(unset)","GOLDIE_GIT_SSH_KNOWN_HOSTS":"(unset)","GOLDIE_GIT_CA_BUNDLE":"(unset)","GOLDIE_GIT_ALLOW_FILE_URL":false,"GOLDIE_SOURCES":"","GOLDIE_POLL_INTERVAL":"1m","GOLDIE_MCP_NAME":"goldie","GOLDIE_GITHUB_CLIENT_ID":"","GOLDIE_GITHUB_CLIENT_SECRET":"(unset)","GOLDIE_GITHUB_URL":"","GOLDIE_GITHUB_ORGS":"","GOLDIE_GITHUB_OPERATOR_TEAMS":"","GOLDIE_PUBLIC_URL":""}` + "\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := loadEnv(c.env)
			if err != nil {
				t.Fatal(err)
			}
			var buf syncBuffer
			cfg.Report(NewLogger(&buf, 0, "server"))
			if got := reportTS.ReplaceAllString(buf.String(), ""); got != c.want {
				t.Errorf("startup report changed:\n got: %s\nwant: %s", got, c.want)
			}
		})
	}
}
