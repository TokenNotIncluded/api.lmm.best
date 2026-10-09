package coreclient

import (
    "bytes"
    "context"
    "net"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"
    pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/metadata"
    "google.golang.org/grpc/status"
    "google.golang.org/protobuf/proto"
)
const testToken = "0123456789abcdef0123456789abcdef"
type testCore struct {
    pb.UnimplementedCoreControlServer
    caps func(context.Context) (*pb.CapabilitiesResponse, error)
    auth func(context.Context) (*pb.AuthorizeResponse, error)
}
func (s *testCore) Capabilities(ctx context.Context, _ *pb.CapabilitiesRequest) (*pb.CapabilitiesResponse, error) {
    if s.caps != nil { return s.caps(ctx) }
    return &pb.CapabilitiesResponse{ProtocolMajor: 1}, nil
}
func (s *testCore) Authorize(ctx context.Context, _ *pb.AuthorizeRequest) (*pb.AuthorizeResponse, error) {
    if s.auth != nil { return s.auth(ctx) }
    return &pb.AuthorizeResponse{UserId: 1}, nil
}
func testClient(t *testing.T, service *testCore) *Client {
    t.Helper()
    dir, err := os.MkdirTemp("", "lmm-rpc-go-")
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = os.RemoveAll(dir) })
    listener, err := net.Listen("unix", filepath.Join(dir, "core.sock"))
    if err != nil { t.Fatal(err) }
    server := grpc.NewServer()
    pb.RegisterCoreControlServer(server, service)
    go func() { _ = server.Serve(listener) }()
    t.Cleanup(server.Stop)
    client, err := New(filepath.Join(dir, "core.sock"), []byte(testToken))
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = client.Close() })
    return client
}
func TestClientReplacesCallerMetadata(t *testing.T) {
    client := testClient(t, &testCore{auth: func(ctx context.Context) (*pb.AuthorizeResponse, error) {
        md, _ := metadata.FromIncomingContext(ctx)
        if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer " + testToken { return nil, status.Error(codes.Unauthenticated, "bad service") }
        if got := md.Get("x-lmm-user-credential"); len(got) != 1 || got[0] != testToken { return nil, status.Error(codes.Unauthenticated, "bad user") }
        if got := md.Get("x-lmm-protocol"); len(got) != 1 || got[0] != "1" { return nil, status.Error(codes.InvalidArgument, "bad version") }
        return &pb.AuthorizeResponse{UserId: 9007199254740993}, nil
    }})
    ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "evil", "x-lmm-protocol", "999", "x-lmm-user-credential", "evil"))
    result, err := client.Authorize(ctx, testToken)
    if err != nil || result.GetUserId() != 9007199254740993 { t.Fatalf("result %v, error %v", result, err) }
}
func TestInheritedDeadlineAndCancellation(t *testing.T) {
    client := testClient(t, &testCore{auth: func(ctx context.Context) (*pb.AuthorizeResponse, error) {
        <-ctx.Done(); return nil, status.FromContextError(ctx.Err()).Err()
    }})
    ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
    defer cancel()
    started := time.Now()
    _, err := client.Authorize(ctx, testToken)
    if status.Code(err) != codes.DeadlineExceeded || time.Since(started) > time.Second { t.Fatalf("deadline not enforced: %v", err) }
    ctx2, cancel2 := context.WithCancel(context.Background()); cancel2()
    _, err = client.Authorize(ctx2, testToken)
    if status.Code(err) != codes.Canceled || len(client.slots) != 0 { t.Fatalf("cancellation leaked a slot: %v", err) }
}
func TestClientDefaultDeadline(t *testing.T) {
    client := testClient(t, &testCore{auth: func(ctx context.Context) (*pb.AuthorizeResponse, error) {
        <-ctx.Done(); return nil, status.FromContextError(ctx.Err()).Err()
    }})
    started := time.Now()
    _, err := client.Authorize(context.Background(), testToken)
    if status.Code(err) != codes.DeadlineExceeded || time.Since(started) > 4*time.Second { t.Fatalf("unbounded call: %v", err) }
    if len(client.slots) != 0 { t.Fatal("deadline leaked a slot") }
}
func TestClientAdmissionAndInputBounds(t *testing.T) {
    client := testClient(t, &testCore{})
    for range maxInFlight { client.slots <- struct{}{} }
    _, err := client.Authorize(context.Background(), testToken)
    if status.Code(err) != codes.ResourceExhausted { t.Fatal(err) }
    for range maxInFlight { <-client.slots }
    _, err = client.Authorize(context.Background(), "")
    if status.Code(err) != codes.Unauthenticated { t.Fatal(err) }
    _, err = client.ListTeams(context.Background(), testToken, -1)
    if status.Code(err) != codes.InvalidArgument { t.Fatal(err) }
    if _, err = New("https://core:9000", []byte(testToken)); err == nil { t.Fatal("accepted non-Unix target") }
    if _, err = New("/tmp/core.sock", []byte("short")); err == nil { t.Fatal("accepted weak service credential") }
}
func TestLargeResponsesAndProtocolMismatchAreRejected(t *testing.T) {
    for _, tc := range []struct{ name string; response *pb.CapabilitiesResponse; code codes.Code }{
        {"oversized", &pb.CapabilitiesResponse{ProtocolMajor: 1, Features: []string{strings.Repeat("x", 2*MaxMessage)}}, codes.ResourceExhausted},
        {"wrong-version", &pb.CapabilitiesResponse{ProtocolMajor: 2}, codes.FailedPrecondition},
    } {
        t.Run(tc.name, func(t *testing.T) {
            client := testClient(t, &testCore{caps: func(context.Context) (*pb.CapabilitiesResponse, error) { return tc.response, nil }})
            _, err := client.Capabilities(context.Background())
            if status.Code(err) != tc.code { t.Fatalf("got %v, want %v", err, tc.code) }
        })
    }
}
func TestOfflineCoreDoesNotBlockConstruction(t *testing.T) {
    dir, err := os.MkdirTemp("", "lmm-rpc-offline-")
    if err != nil { t.Fatal(err) }; defer os.RemoveAll(dir)
    client, err := New(filepath.Join(dir, "absent.sock"), []byte(testToken))
    if err != nil { t.Fatal(err) }; defer client.Close()
    _, err = client.Capabilities(context.Background())
    if status.Code(err) != codes.Unavailable { t.Fatal(err) }
}
func TestProtobufLargeIDsUnknownFieldsAndMalformedData(t *testing.T) {
    wire := []byte{0x08,0x02,0x10,0x81,0x80,0x80,0x80,0x80,0x80,0x80,0x10}
    var value pb.Account
    if err := proto.Unmarshal(wire, &value); err != nil { t.Fatal(err) }
    if value.Id != 9007199254740993 || value.Kind != pb.AccountKind_ACCOUNT_KIND_TEAM { t.Fatal(&value) }
    encoded, err := proto.Marshal(&value)
    if err != nil || !bytes.Equal(encoded, wire) { t.Fatal("wire format changed") }
    future := append(append([]byte{}, wire...), 0x98,0x06,0x01)
    if err := proto.Unmarshal(future, &value); err != nil || value.Id != 9007199254740993 { t.Fatal("unknown field broke decoding") }
    if err := proto.Unmarshal([]byte{0x10,0x80}, &value); err == nil { t.Fatal("accepted truncated varint") }
}
