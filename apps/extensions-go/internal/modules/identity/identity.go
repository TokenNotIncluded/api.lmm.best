// Package identity exposes the read-only native core contract to internal callers.
// The host checks its service token. User operations additionally require an
// actual user credential verified by Rust; no user IDs or roles are trusted.
package identity

import (
    "context"
    "net/http"
    "strconv"

    pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
    "google.golang.org/protobuf/encoding/protojson"
    "google.golang.org/protobuf/proto"
)

type Core interface {
    Capabilities(context.Context) (*pb.CapabilitiesResponse, error)
    Authorize(context.Context, string) (*pb.AuthorizeResponse, error)
    ListTeams(context.Context, string, int64) (*pb.ListTeamsResponse, error)
}
type Module struct { core Core }
func New(core Core) *Module { return &Module{core: core} }
func (*Module) Name() string { return "identity" }
func (m *Module) Handler() http.Handler {
    mux := http.NewServeMux()
    mux.HandleFunc("GET /capabilities", func(w http.ResponseWriter, r *http.Request) {
        value, err := m.core.Capabilities(r.Context()); respond(w, value, err)
    })
    mux.HandleFunc("GET /self", func(w http.ResponseWriter, r *http.Request) {
        credential, err := userCredential(r)
        if err != nil { respond(w, nil, err); return }
        value, err := m.core.Authorize(r.Context(), credential); respond(w, value, err)
    })
    mux.HandleFunc("GET /teams", func(w http.ResponseWriter, r *http.Request) {
        credential, err := userCredential(r)
        if err != nil { respond(w, nil, err); return }
        var after int64
        if values, exists := r.URL.Query()["after_id"]; exists {
            if len(values) != 1 { respond(w, nil, status.Error(codes.InvalidArgument, "invalid cursor")); return }
            after, err = strconv.ParseInt(values[0], 10, 64)
            if err != nil || after < 0 { respond(w, nil, status.Error(codes.InvalidArgument, "invalid cursor")); return }
        }
        value, err := m.core.ListTeams(r.Context(), credential, after); respond(w, value, err)
    })
    return mux
}
func userCredential(r *http.Request) (string, error) {
    values := r.Header.Values("X-LMM-User-Credential")
    if len(values) != 1 || values[0] == "" || len(values[0]) > 256 {
        return "", status.Error(codes.Unauthenticated, "missing or invalid user credential")
    }
    return values[0], nil
}
func respond(w http.ResponseWriter, value proto.Message, err error) {
    w.Header().Set("Cache-Control", "no-store")
    w.Header().Set("Content-Type", "application/json")
    if err != nil {
        code := http.StatusBadGateway
        label := "core_unavailable"
        switch status.Code(err) {
        case codes.Unauthenticated: code, label = 401, "unauthorized"
        case codes.PermissionDenied: code, label = 403, "forbidden"
        case codes.InvalidArgument: code, label = 400, "invalid_request"
        case codes.FailedPrecondition, codes.Aborted: code, label = 409, "core_contract_conflict"
        case codes.ResourceExhausted: code, label = 429, "core_busy"
        case codes.Canceled: code, label = 408, "request_canceled"
        case codes.DeadlineExceeded: code, label = 504, "core_timeout"
        case codes.Unavailable: code = 503
        }
        w.WriteHeader(code)
        _, _ = w.Write([]byte(`{"error":"` + label + `"}`))
        return
    }
    data, marshalErr := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(value)
    if marshalErr != nil {
        w.WriteHeader(http.StatusBadGateway)
        _, _ = w.Write([]byte(`{"error":"invalid_core_response"}`))
        return
    }
    _, _ = w.Write(data)
}
