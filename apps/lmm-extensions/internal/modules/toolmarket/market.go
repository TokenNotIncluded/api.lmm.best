package toolmarket

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

type Config struct {
	CallbackURL    string
	Timeout        time.Duration
	CallsPerMinute int
	Funds          Funds
}
type Module struct {
	store     *Store
	authority Authority
	funds     Funds
	net       *network
	callback  string
	timeout   time.Duration
	perMinute int
	now       func() time.Time
	mu        sync.Mutex
	buckets   map[string]bucket
	active    chan struct{}
}
type bucket struct {
	Start time.Time
	Count int
}

func New(store *Store, authority Authority, cfg Config) (*Module, error) {
	if store == nil || authority == nil {
		return nil, ErrInvalid
	}
	n := newNetwork()
	if _, e := n.url(cfg.CallbackURL); e != nil {
		return nil, invalid("fixed HTTPS callback required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.Timeout < time.Millisecond || cfg.Timeout > 30*time.Second {
		return nil, ErrInvalid
	}
	if cfg.CallsPerMinute == 0 {
		cfg.CallsPerMinute = 60
	}
	if cfg.CallsPerMinute < 1 || cfg.CallsPerMinute > 600 {
		return nil, ErrInvalid
	}
	return &Module{store: store, authority: authority, funds: cfg.Funds, net: n, callback: cfg.CallbackURL, timeout: cfg.Timeout, perMinute: cfg.CallsPerMinute, now: time.Now, buckets: map[string]bucket{}, active: make(chan struct{}, 8)}, nil
}
func (*Module) Name() string { return "toolmarket" }
func (m *Module) admit(p Principal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for k, b := range m.buckets {
		if now.Sub(b.Start) >= time.Minute {
			delete(m.buckets, k)
		}
	}
	b, ok := m.buckets[p.key()]
	if !ok {
		if len(m.buckets) >= 4096 {
			return ErrLimit
		}
		b.Start = now
	}
	if b.Count >= m.perMinute {
		return ErrLimit
	}
	b.Count++
	m.buckets[p.key()] = b
	return nil
}

type RegisterInput struct {
	Endpoint string `json:"endpoint"`
	Auth     string `json:"auth"`
	Header   string `json:"header,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	Issuer   string `json:"issuer,omitempty"`
	Scopes   string `json:"scopes,omitempty"`
}
type InstallInput struct {
	Release string `json:"release"`
}
type InstallationInput struct {
	Installation string `json:"installation"`
}
type UpgradeInput struct {
	Installation string `json:"installation"`
	Release      string `json:"release"`
}
type EnableInput struct {
	Installation string `json:"installation"`
	Enabled      bool   `json:"enabled"`
}
type KeyInput struct {
	Installation string `json:"installation"`
	Key          string `json:"key"`
}
type ReleaseInput struct {
	Server  string           `json:"server"`
	Version string           `json:"version"`
	Prices  map[string]Price `json:"prices"`
}
type PublishInput struct {
	Release   string `json:"release"`
	Published bool   `json:"published"`
}
type SearchInput struct {
	Query  string `json:"query"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}
type ToolInput struct {
	Installation string `json:"installation"`
	Name         string `json:"name"`
}
type ExecuteInput struct {
	Installation   string          `json:"installation"`
	Name           string          `json:"name"`
	Handle         string          `json:"handle"`
	Quote          string          `json:"quote"`
	IdempotencyKey string          `json:"idempotency_key"`
	Arguments      json.RawMessage `json:"arguments"`
}
type Quote struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Expires   int64  `json:"expires"`
	Reason    string `json:"reason,omitempty"`
}

func quoteFor(r Release, t Tool, now time.Time) Quote {
	p, ok := r.Prices[t.Name]
	if !ok {
		return Quote{Reason: "price_unavailable"}
	}
	amount, e := p.total(now)
	q := Quote{ID: digest([]any{r.ID, t.Name, p}), Amount: amount, Currency: p.Currency, Expires: p.ValidUntil, Available: e == nil}
	if e != nil {
		q.Reason = "price_unavailable"
	}
	return q
}
func (m *Module) register(p Principal, in RegisterInput) (any, error) {
	if _, e := m.net.url(in.Endpoint); e != nil {
		return nil, e
	}
	if in.Auth != "none" && in.Auth != "api_key" && in.Auth != "oauth" {
		return nil, ErrInvalid
	}
	if in.Auth == "api_key" {
		if in.Header == "" {
			in.Header = "Authorization"
		}
		if in.Header != "Authorization" && in.Header != "X-Api-Key" && in.Header != "Api-Key" {
			return nil, ErrInvalid
		}
	} else if in.Header != "" {
		return nil, ErrInvalid
	}
	if in.Auth == "oauth" {
		if in.ClientID == "" || len(in.ClientID) > 512 || len(in.Scopes) > 512 || strings.ContainsAny(in.Scopes+in.ClientID, "\r\n\x00") {
			return nil, ErrInvalid
		}
		if in.Issuer != "" {
			if _, e := m.net.url(in.Issuer); e != nil {
				return nil, e
			}
		}
	} else if in.ClientID != "" || in.Issuer != "" || in.Scopes != "" {
		return nil, ErrInvalid
	}
	id, e := randomID()
	if e != nil {
		return nil, e
	}
	iid, e := randomID()
	if e != nil {
		return nil, e
	}
	s := Server{ID: id, Owner: p, Endpoint: in.Endpoint, Auth: in.Auth, Header: in.Header, ClientID: in.ClientID, Issuer: in.Issuer, Scopes: in.Scopes, Revision: 1, Tools: []Tool{}}
	inst := Installation{ID: iid, Owner: p, Server: id, Enabled: true, Generation: 1, Loaded: map[string]string{}}
	e = m.store.update(func(d *database) error {
		if len(d.Servers) >= 10000 || len(d.Installs) >= 20000 {
			return ErrLimit
		}
		d.Servers[id] = s
		d.Installs[iid] = inst
		d.audit(p, "register", id, "ok")
		return nil
	})
	return map[string]any{"server": s, "installation": inst}, e
}
func selected(d database, p Principal, id string) (Installation, Release, Secret, error) {
	i, ok := d.Installs[id]
	if !ok || i.Owner != p {
		return i, Release{}, Secret{}, ErrMissing
	}
	if !i.Enabled {
		return i, Release{}, Secret{}, ErrDenied
	}
	if i.Release == "" {
		s, ok := d.Servers[i.Server]
		if !ok || s.Owner != p {
			return i, Release{}, Secret{}, ErrMissing
		}
		return i, Release{Server: s}, d.Secrets[id], nil
	}
	r, ok := d.Releases[i.Release]
	if !ok || r.Server.ID != i.Server {
		return i, r, Secret{}, ErrConflict
	}
	return i, r, d.Secrets[id], nil
}
func (m *Module) snapshot(p Principal, id string) (i Installation, r Release, s Secret, e error) {
	if !validID.MatchString(id) {
		e = ErrInvalid
		return
	}
	e = m.store.view(func(d database) error { var e error; i, r, s, e = selected(d, p, id); return e })
	return
}
func (m *Module) setKey(p Principal, in KeyInput) error {
	if len(in.Key) < 1 || len(in.Key) > 8192 || strings.ContainsAny(in.Key, "\r\n\x00") {
		return ErrInvalid
	}
	return m.store.update(func(d *database) error {
		i, r, _, e := selected(*d, p, in.Installation)
		if e != nil {
			return e
		}
		if r.Server.Auth != "api_key" {
			return ErrInvalid
		}
		i.Generation++
		i.Loaded = map[string]string{}
		d.Installs[i.ID] = i
		d.Secrets[i.ID] = Secret{Access: in.Key}
		d.audit(p, "key.set", i.ID, "ok")
		return nil
	})
}
func (m *Module) discover(ctx context.Context, p Principal, id string) (any, error) {
	i, r, s, e := m.snapshot(p, id)
	if e != nil {
		return nil, e
	}
	if r.Server.Owner != p {
		return nil, ErrDenied
	}
	c, e := m.connect(ctx, r.Server, s)
	if e != nil {
		return nil, e
	}
	defer c.close(ctx)
	tools, e := c.tools(ctx)
	if e != nil {
		return nil, e
	}
	e = m.store.update(func(d *database) error {
		current, _, _, e := selected(*d, p, id)
		if e != nil {
			return e
		}
		srv := d.Servers[r.Server.ID]
		if current.Generation != i.Generation || srv.Revision != r.Server.Revision {
			return ErrConflict
		}
		srv.Tools = tools
		srv.Revision++
		d.Servers[srv.ID] = srv
		d.audit(p, "discover", id, "ok")
		return nil
	})
	return map[string]any{"count": len(tools), "server": r.Server.ID}, e
}
func (m *Module) release(p Principal, in ReleaseInput) (any, error) {
	if !validVersion.MatchString(in.Version) || !validID.MatchString(in.Server) {
		return nil, ErrInvalid
	}
	var r Release
	e := m.store.update(func(d *database) error {
		s, ok := d.Servers[in.Server]
		if !ok || s.Owner != p {
			return ErrMissing
		}
		if len(s.Tools) == 0 || len(in.Prices) != len(s.Tools) {
			return ErrInvalid
		}
		for _, t := range s.Tools {
			price, ok := in.Prices[t.Name]
			if !ok {
				return ErrInvalid
			}
			if price.Kind == "unknown" {
				if price.Amount != 0 || price.Multiplier < 10000 || price.Multiplier > 1000000 {
					return ErrInvalid
				}
			} else {
				if _, e := price.total(m.now()); e != nil {
					return e
				}
				if _, e := m.net.url(price.Source); e != nil {
					return e
				}
			}
		}
		if len(d.Releases) >= 20000 {
			return ErrLimit
		}
		id := digest([]string{in.Server, in.Version})
		if _, ok := d.Releases[id]; ok {
			return ErrConflict
		}
		r = Release{ID: id, Server: s, Version: in.Version, Prices: in.Prices}
		d.Releases[id] = r
		d.audit(p, "release", id, "ok")
		return nil
	})
	return r, e
}
func (m *Module) publish(p Principal, in PublishInput) error {
	return m.store.update(func(d *database) error {
		r, ok := d.Releases[in.Release]
		if !ok || r.Server.Owner != p {
			return ErrMissing
		}
		r.Published = in.Published
		d.Releases[in.Release] = r
		d.audit(p, "publish", r.ID, "ok")
		return nil
	})
}
func (m *Module) install(p Principal, in InstallInput) (any, error) {
	id, e := randomID()
	if e != nil {
		return nil, e
	}
	var inst Installation
	e = m.store.update(func(d *database) error {
		r, ok := d.Releases[in.Release]
		if !ok || !r.Published && r.Server.Owner != p {
			return ErrMissing
		}
		if len(d.Installs) >= 20000 {
			return ErrLimit
		}
		inst = Installation{ID: id, Owner: p, Release: r.ID, Server: r.Server.ID, Enabled: true, Generation: 1, Loaded: map[string]string{}}
		d.Installs[id] = inst
		d.audit(p, "install", id, "ok")
		return nil
	})
	return inst, e
}
func invalidate(d *database, i *Installation) {
	i.Generation++
	i.Loaded = map[string]string{}
	for k, s := range d.States {
		if s.Install == i.ID {
			delete(d.States, k)
		}
	}
}
func (m *Module) enable(p Principal, in EnableInput) error {
	return m.store.update(func(d *database) error {
		i, ok := d.Installs[in.Installation]
		if !ok || i.Owner != p {
			return ErrMissing
		}
		invalidate(d, &i)
		i.Enabled = in.Enabled
		d.Installs[i.ID] = i
		d.audit(p, "enable", i.ID, "ok")
		return nil
	})
}
func (m *Module) upgrade(p Principal, in UpgradeInput) error {
	return m.store.update(func(d *database) error {
		i, ok := d.Installs[in.Installation]
		if !ok || i.Owner != p {
			return ErrMissing
		}
		r, ok := d.Releases[in.Release]
		if !ok || r.Server.ID != i.Server || !r.Published && r.Server.Owner != p {
			return ErrMissing
		}
		invalidate(d, &i)
		i.Release = r.ID
		delete(d.Secrets, i.ID)
		d.Installs[i.ID] = i
		d.audit(p, "upgrade", i.ID, "ok")
		return nil
	})
}
func findTool(r Release, name string) (Tool, error) {
	if !validTool.MatchString(name) {
		return Tool{}, ErrInvalid
	}
	for _, t := range r.Server.Tools {
		if t.Name == name {
			return t, nil
		}
	}
	return Tool{}, ErrMissing
}
func searchBounds(in *SearchInput) error {
	if len(in.Query) > 200 || in.Offset < 0 || in.Offset > 20000 {
		return ErrInvalid
	}
	if in.Limit == 0 {
		in.Limit = 5
	}
	if in.Limit < 1 || in.Limit > 20 {
		return ErrInvalid
	}
	return nil
}
func (m *Module) search(p Principal, in SearchInput) (any, error) {
	if e := searchBounds(&in); e != nil {
		return nil, e
	}
	type hit struct {
		Installation string `json:"installation"`
		Name         string `json:"name"`
		Description  string `json:"description"`
		Version      string `json:"version"`
	}
	hits := []hit{}
	e := m.store.view(func(d database) error {
		for id, i := range d.Installs {
			if i.Owner != p || !i.Enabled || i.Release == "" {
				continue
			}
			r := d.Releases[i.Release]
			for _, t := range r.Server.Tools {
				if strings.Contains(strings.ToLower(t.Name+" "+t.Description), strings.ToLower(in.Query)) {
					hits = append(hits, hit{id, t.Name, short(t.Description, 120), r.Version})
				}
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Installation+hits[i].Name < hits[j].Installation+hits[j].Name })
	start := min(in.Offset, len(hits))
	end := min(start+in.Limit, len(hits))
	return map[string]any{"items": hits[start:end], "has_more": end < len(hits), "untrusted_content": true}, nil
}
func (m *Module) catalog(p Principal, in SearchInput) (any, error) {
	if e := searchBounds(&in); e != nil {
		return nil, e
	}
	type item struct {
		Endpoint string `json:"endpoint"`
		Auth     string `json:"auth"`
		Release  string `json:"release"`
		Server   string `json:"server"`
		Version  string `json:"version"`
		Tools    int    `json:"tool_count"`
	}
	items := []item{}
	e := m.store.view(func(d database) error {
		for _, r := range d.Releases {
			if r.Published || r.Server.Owner == p {
				if strings.Contains(strings.ToLower(r.Server.Endpoint+" "+r.Version), strings.ToLower(in.Query)) {
					items = append(items, item{r.Server.Endpoint, r.Server.Auth, r.ID, r.Server.ID, r.Version, len(r.Server.Tools)})
				}
			}
		}
		return nil
	})
	sort.Slice(items, func(i, j int) bool { return items[i].Release < items[j].Release })
	start := min(in.Offset, len(items))
	end := min(start+in.Limit, len(items))
	return map[string]any{"items": items[start:end], "has_more": end < len(items)}, e
}
func (m *Module) details(p Principal, in ToolInput, load bool) (any, error) {
	i, r, _, e := m.snapshot(p, in.Installation)
	if e != nil {
		return nil, e
	}
	if i.Release == "" {
		return nil, ErrConflict
	}
	t, e := findTool(r, in.Name)
	if e != nil {
		return nil, e
	}
	q := quoteFor(r, t, m.now())
	out := map[string]any{"tool": t, "quote": q, "price_rule": r.Prices[t.Name], "version": r.Version, "untrusted_content": true}
	if load {
		handle := digest([]any{p, i.ID, i.Generation, r.ID, t})
		e = m.store.update(func(d *database) error {
			current, _, _, e := selected(*d, p, i.ID)
			if e != nil {
				return e
			}
			if current.Generation != i.Generation {
				return ErrConflict
			}
			current.Loaded[t.Name] = handle
			d.Installs[i.ID] = current
			d.audit(p, "load", i.ID, "ok")
			return nil
		})
		if e != nil {
			return nil, e
		}
		out["handle"] = handle
	}
	return out, nil
}

// installations returns no credentials, loaded handles or provider schemas.
func (m *Module) installations(p Principal, in SearchInput) (any, error) {
	if err := searchBounds(&in); err != nil {
		return nil, err
	}
	type item struct {
		ID       string `json:"id"`
		Release  string `json:"release"`
		Server   string `json:"server"`
		Endpoint string `json:"endpoint"`
		Auth     string `json:"auth"`
		Version  string `json:"version"`
		Enabled  bool   `json:"enabled"`
	}
	items := []item{}
	err := m.store.view(func(d database) error {
		for _, i := range d.Installs {
			if i.Owner != p {
				continue
			}
			s := d.Servers[i.Server]
			version := "draft"
			if i.Release != "" {
				r := d.Releases[i.Release]
				s = r.Server
				version = r.Version
			}
			if strings.Contains(strings.ToLower(i.ID+" "+s.Endpoint+" "+version), strings.ToLower(in.Query)) {
				items = append(items, item{i.ID, i.Release, i.Server, s.Endpoint, s.Auth, version, i.Enabled})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	start := min(in.Offset, len(items))
	end := min(start+in.Limit, len(items))
	return map[string]any{"items": items[start:end], "has_more": end < len(items)}, nil
}
