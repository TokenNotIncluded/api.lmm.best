package support

import (
	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/store"
	"net/http"
)

func (*Service) Name() string { return "support" }
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /conversations", func(w http.ResponseWriter, r *http.Request) {
		var c OpenRequest
		if err := store.Decode(w, r, &c); err != nil {
			store.Respond(w, nil, err)
			return
		}
		token, err := store.Credential(r)
		if err != nil {
			store.Respond(w, nil, err)
			return
		}
		v, err := s.Open(r.Context(), token, c)
		store.Respond(w, v, err)
	})
	mux.HandleFunc("POST /conversations/change", func(w http.ResponseWriter, r *http.Request) {
		var c ConversationChange
		if err := store.Decode(w, r, &c); err != nil {
			store.Respond(w, nil, err)
			return
		}
		token, err := store.Credential(r)
		if err != nil {
			store.Respond(w, nil, err)
			return
		}
		v, err := s.Change(r.Context(), token, c)
		store.Respond(w, v, err)
	})
	mux.HandleFunc("POST /customers/change", func(w http.ResponseWriter, r *http.Request) {
		var c CustomerChange
		if err := store.Decode(w, r, &c); err != nil {
			store.Respond(w, nil, err)
			return
		}
		token, err := store.Credential(r)
		if err != nil {
			store.Respond(w, nil, err)
			return
		}
		v, err := s.UpdateCustomer(r.Context(), token, c)
		store.Respond(w, v, err)
	})
	mux.HandleFunc("GET /shops/{shop}/conversations/{conversation}", func(w http.ResponseWriter, r *http.Request) {
		token, err := store.Credential(r)
		if err != nil {
			store.Respond(w, nil, err)
			return
		}
		after, limit, err := store.Pagination(r)
		if err != nil {
			store.Respond(w, nil, err)
			return
		}
		v, err := s.Thread(r.Context(), token, r.PathValue("shop"), r.PathValue("conversation"), after, limit)
		store.Respond(w, v, err)
	})
	mux.HandleFunc("GET /shops/{shop}/manage/{kind}", func(w http.ResponseWriter, r *http.Request) {
		token, err := store.Credential(r)
		if err != nil {
			store.Respond(w, nil, err)
			return
		}
		after, limit, err := store.Pagination(r)
		if err != nil {
			store.Respond(w, nil, err)
			return
		}
		v, err := s.ManageList(r.Context(), token, r.PathValue("shop"), r.PathValue("kind"), after, limit)
		store.Respond(w, v, err)
	})
	return store.BoundedHTTP(mux)
}
