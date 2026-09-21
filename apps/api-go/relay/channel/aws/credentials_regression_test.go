package aws

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/stretchr/testify/require"
)

func TestAwsSDKCredentialTransport(t *testing.T) {
	service.InitHttpClient()
	for _, tc := range []struct {
		name, secret string
		kind         dto.AwsKeyType
	}{
		{"bearer", "synthetic-token|us-east-1", dto.AwsKeyTypeApiKey},
		{"signed", "synthetic-access|synthetic-secret|us-east-1", dto.AwsKeyTypeAKSK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				c := newAwsTestContext(httptest.NewRecorder(), context.Background())
				info := newAwsTestRelayInfo()
				info.ApiKey = tc.secret
				info.ChannelOtherSettings.AwsKeyType = tc.kind
				info.IsStream = stream
				a := &Adaptor{}
				a.Init(info)
				_, err := a.DoRequest(c, info, strings.NewReader(`{"messages":[{"role":"user","content":"hello"}],"max_tokens":16}`))
				require.NoError(t, err)
				require.NotNil(t, a.AwsClient)
				if stream {
					require.IsType(t, &bedrockruntime.InvokeModelWithResponseStreamInput{}, a.AwsReq)
					continue
				}
				calls := 0
				_, err = a.AwsClient.InvokeModel(context.Background(), a.AwsReq.(*bedrockruntime.InvokeModelInput), func(options *bedrockruntime.Options) {
					options.HTTPClient = awsHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
						calls++
						// The real SDK serializes and authenticates before this fake transport.
						require.Equal(t, "bedrock-runtime.us-east-1.amazonaws.com", request.URL.Host)
						require.True(t, strings.HasSuffix(request.URL.Path, "/invoke"))
						auth := request.Header.Get("Authorization")
						require.NotContains(t, auth, "|us-east-1")
						if tc.kind == dto.AwsKeyTypeApiKey {
							require.Equal(t, "Bearer synthetic-token", auth)
						} else {
							require.Contains(t, auth, "AWS4-HMAC-SHA256")
						}
						body, readErr := io.ReadAll(request.Body)
						require.NoError(t, readErr)
						require.Contains(t, string(body), "hello")
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
					})
				})
				require.NoError(t, err)
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestAwsCredentialValidation(t *testing.T) {
	for _, secret := range []string{"", "|us-east-1", "token|", "access||us-east-1", "access|secret|", "a|b|c|d", "token| us-east-1"} {
		_, err := parseAwsCredentials(secret, "")
		require.Error(t, err, secret)
	}
	_, err := parseAwsCredentials("token|us-east-1", dto.AwsKeyTypeAKSK)
	require.Error(t, err)
	_, err = parseAwsCredentials("access|secret|us-east-1", dto.AwsKeyTypeApiKey)
	require.Error(t, err)
	_, err = parseAwsCredentials("token|us-east-1", "unknown")
	require.Error(t, err)
}
