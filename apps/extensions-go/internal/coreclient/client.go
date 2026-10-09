// Package coreclient is the only core access path for Go extension modules.
// It has no SQL dependency, no authorization cache and no model relay.
package coreclient

import (
    "context"
    "errors"
    "io"
    "net"
    "os"
    "path/filepath"
    "strings"
    "time"

    pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/credentials/insecure"
    "google.golang.org/grpc/metadata"
    "google.golang.org/grpc/status"
)

const MaxMessage = 64 * 1024
const Timeout = 2 * time.Second
const maxInFlight = 32

type Client struct {
    connection *grpc.ClientConn
    rpc pb.CoreControlClient
    authorization string
    slots chan struct{}
}
func ReadTokenFile(path string) ([]byte, error) {
    f, err := os.Open(path)
    if err != nil { return nil, errors.New("cannot open core RPC credential file") }
    data, readErr := io.ReadAll(io.LimitReader(f, 4097))
    closeErr := f.Close()
    if readErr != nil || closeErr != nil || len(data) > 4096 { return nil, errors.New("cannot read bounded core RPC credential file") }
    token := strings.TrimSpace(string(data))
    if !validToken(token) { return nil, errors.New("core RPC credential must contain 32 to 256 visible ASCII bytes") }
    return []byte(token), nil
}
func validToken(token string) bool {
    if len(token) < 32 || len(token) > 256 { return false }
    for i := 0; i < len(token); i++ { if token[i] < 33 || token[i] > 126 { return false } }
    return true
}
func validUserCredential(token string) bool {
    if len(token) < 32 || len(token) > 256 { return false }
    for i := 0; i < len(token); i++ {
        b := token[i]
        if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-') { return false }
    }
    return true
}
// New is lazy: an offline core does not prevent other modules starting.
// This dialer always uses Unix sockets; callers cannot select plaintext TCP.
func New(socket string, token []byte) (*Client, error) {
    if !filepath.IsAbs(socket) || len(socket) > 100 || strings.ContainsRune(socket, 0) {
        return nil, errors.New("core RPC requires a short absolute Unix socket path")
    }
    if !validToken(string(token)) { return nil, errors.New("invalid core RPC service credential") }
    c := &Client{authorization: "Bearer " + string(token), slots: make(chan struct{}, maxInFlight)}
    conn, err := grpc.NewClient("passthrough:///lmm-core",
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
            var dialer net.Dialer
            return dialer.DialContext(ctx, "unix", socket)
        }),
        grpc.WithDisableRetry(), grpc.WithDisableServiceConfig(),
        grpc.WithMaxHeaderListSize(8192),
        grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessage), grpc.MaxCallSendMsgSize(MaxMessage), grpc.WaitForReady(false)),
        grpc.WithUnaryInterceptor(c.boundCall),
    )
    if err != nil { return nil, err }
    c.connection = conn
    c.rpc = pb.NewCoreControlClient(conn)
    return c, nil
}
func (c *Client) Close() error { return c.connection.Close() }
func (c *Client) boundCall(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
    if err := ctx.Err(); err != nil { return status.FromContextError(err).Err() }
    select {
    case c.slots <- struct{}{}:
        defer func() { <-c.slots }()
    default:
        return status.Error(codes.ResourceExhausted, "core RPC client is busy")
    }
    bounded, cancel := context.WithTimeout(ctx, Timeout)
    defer cancel()
    return invoke(bounded, method, req, reply, cc, opts...)
}
func (c *Client) context(ctx context.Context, user string) context.Context {
    values := metadata.Pairs("authorization", c.authorization, "x-lmm-protocol", "1")
    if user != "" { values.Set("x-lmm-user-credential", user) }
    // Do not forward caller-supplied credentials, versions or retry hints.
    return metadata.NewOutgoingContext(ctx, values)
}
func (c *Client) Capabilities(ctx context.Context) (*pb.CapabilitiesResponse, error) {
    result, err := c.rpc.Capabilities(c.context(ctx, ""), &pb.CapabilitiesRequest{})
    if err == nil && result.GetProtocolMajor() != 1 { return nil, status.Error(codes.FailedPrecondition, "unsupported core protocol major") }
    return result, err
}
func (c *Client) Authorize(ctx context.Context, credential string) (*pb.AuthorizeResponse, error) {
    if !validUserCredential(credential) { return nil, status.Error(codes.Unauthenticated, "invalid user credential") }
    return c.rpc.Authorize(c.context(ctx, credential), &pb.AuthorizeRequest{})
}
func (c *Client) ListTeams(ctx context.Context, credential string, after int64) (*pb.ListTeamsResponse, error) {
    if !validUserCredential(credential) { return nil, status.Error(codes.Unauthenticated, "invalid user credential") }
    if after < 0 { return nil, status.Error(codes.InvalidArgument, "after_id must be nonnegative") }
    return c.rpc.ListTeams(c.context(ctx, credential), &pb.ListTeamsRequest{AfterId: after})
}
