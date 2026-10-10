package promotions

import (
	"net/http"

	"github.com/TokenNotIncluded/api.lmm.best/extensions/internal/modules/assistant/access"
)

func (*Service) Name() string { return "promotions" }
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /limits", func(w http.ResponseWriter, r *http.Request) {
		token, e := access.Credential(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		v, e := s.Limits(r.Context(), token)
		access.Respond(w, v, e)
	})
	for _, route := range []string{"eligibility", "apply"} {
		mux.HandleFunc("POST /"+route, func(w http.ResponseWriter, r *http.Request) {
			token, e := access.Credential(r)
			if e != nil {
				access.Respond(w, nil, e)
				return
			}
			var in struct {
				Campaign    string `json:"campaign"`
				Kind        string `json:"kind"`
				EvidenceRef string `json:"evidence_ref,omitempty"`
			}
			if e = access.Body(w, r, &in); e != nil {
				access.Respond(w, nil, e)
				return
			}
			if route == "eligibility" {
				v, e := s.Eligibility(r.Context(), token, in.Campaign, in.EvidenceRef, in.Kind)
				access.Respond(w, v, e)
			} else {
				v, e := s.Apply(r.Context(), token, in.Campaign, in.EvidenceRef, in.Kind)
				value, e := Result(v, e)
				access.Respond(w, value, e)
			}
		})
	}
	mux.HandleFunc("GET /claims/{key}", func(w http.ResponseWriter, r *http.Request) {
		token, e := access.Credential(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		v, e := s.Read(r.Context(), token, r.PathValue("key"))
		access.Respond(w, v, e)
	})
	mux.HandleFunc("POST /claims/{key}/reconcile", func(w http.ResponseWriter, r *http.Request) {
		token, e := access.Credential(r)
		if e != nil {
			access.Respond(w, nil, e)
			return
		}
		v, e := s.Reconcile(r.Context(), token, r.PathValue("key"))
		value, e := Result(v, e)
		access.Respond(w, value, e)
	})
	return mux
}
