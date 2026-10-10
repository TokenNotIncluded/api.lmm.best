package assistant

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
)

func (*Service) Name() string { return "assistant" }
func accountFor(teamID int64) (access.Account, error) {
	if teamID < 0 {
		return access.Account{}, access.ErrInvalid
	}
	if teamID == 0 {
		return access.Account{}, nil
	}
	return access.Account{Kind: "team", ID: teamID}, nil
}
func queryAccount(r *http.Request) (access.Account, error) {
	v := r.URL.Query()["team_id"]
	if len(v) == 0 {
		return access.Account{}, nil
	}
	if len(v) != 1 {
		return access.Account{}, access.ErrInvalid
	}
	n, e := strconv.ParseInt(v[0], 10, 64)
	if e != nil {
		return access.Account{}, access.ErrInvalid
	}
	return accountFor(n)
}
func query(r *http.Request, name string) (string, error) {
	v := r.URL.Query()[name]
	if len(v) > 1 {
		return "", access.ErrInvalid
	}
	if len(v) == 0 {
		return "", nil
	}
	return v[0], nil
}
func limitFor(r *http.Request, defaultLimit, max int) (int, error) {
	s, e := query(r, "limit")
	if e != nil {
		return 0, e
	}
	if s == "" {
		return defaultLimit, nil
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 || n > max {
		return 0, access.ErrInvalid
	}
	return n, nil
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", func(w http.ResponseWriter, r *http.Request) {
		token, e := access.Credential(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		var in struct {
			TeamID int64 `json:"team_id"`
		}
		if e = access.Body(w, r, &in); e != nil {
			access.Respond(w, nil, e)
			return
		}
		account, e := accountFor(in.TeamID)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		v, e := s.Start(r.Context(), token, account)
		access.Respond(w, v, e)
	})
	mux.HandleFunc("GET /tools", func(w http.ResponseWriter, r *http.Request) {
		token, e := access.Credential(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		account, e := queryAccount(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		p, e := s.check(r.Context(), token, account, access.Read)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		q, e := query(r, "q")
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		after, e := query(r, "after")
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		limit, e := limitFor(r, 8, 20)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		tools, next, e := s.registry.Discover(p, q, after, limit)
		access.Respond(w, struct {
			Tools []Summary `json:"tools"`
			Next  string    `json:"next,omitempty"`
		}{tools, next}, e)
	})
	mux.HandleFunc("GET /tools/{name}", func(w http.ResponseWriter, r *http.Request) {
		token, e := access.Credential(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		account, e := queryAccount(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		p, e := s.check(r.Context(), token, account, access.Read)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		v, e := s.registry.Describe(p, r.PathValue("name"))
		access.Respond(w, v, e)
	})
	mux.HandleFunc("POST /invoke", func(w http.ResponseWriter, r *http.Request) {
		token, e := access.Credential(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		var in struct {
			TeamID    int64           `json:"team_id"`
			SessionID string          `json:"session_id"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if e = access.Body(w, r, &in); e != nil {
			access.Respond(w, nil, e)
			return
		}
		account, e := accountFor(in.TeamID)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		v, e := s.Invoke(r.Context(), token, account, in.SessionID, in.CallID, in.Name, in.Arguments)
		access.Respond(w, v, e)
	})
	mux.HandleFunc("POST /chat", func(w http.ResponseWriter, r *http.Request) {
		token, e := access.Credential(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		var in struct {
			TeamID    int64  `json:"team_id"`
			SessionID string `json:"session_id"`
			Input     string `json:"input"`
		}
		if e = access.Body(w, r, &in); e != nil {
			access.Respond(w, nil, e)
			return
		}
		account, e := accountFor(in.TeamID)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		v, e := s.Chat(r.Context(), token, account, in.SessionID, in.Input)
		access.Respond(w, v, e)
	})
	mux.HandleFunc("GET /entries", func(w http.ResponseWriter, r *http.Request) {
		token, e := access.Credential(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		account, e := queryAccount(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		after, e := query(r, "after")
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		limit, e := limitFor(r, 20, 100)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		v, e := s.Entries(r.Context(), token, account, after, limit)
		access.Respond(w, v, e)
	})
	return mux
}
