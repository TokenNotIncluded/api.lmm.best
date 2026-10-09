package model

// These are public native-client registrations, not credentials. Client IDs
// are content-addressed; reconnecting with another loopback port reuses the row.
type OAuthServerMCPClient struct {
	ID           string   `gorm:"primaryKey;size:128"`
	Issuer       string   `gorm:"not null;size:512;index"`
	Name         string   `gorm:"not null;size:128"`
	RedirectURIs []string `gorm:"serializer:json;type:text;not null"`
	Scope        string   `gorm:"not null;size:256"`
	CreatedAtMs  int64    `gorm:"not null"`
}

func (OAuthServerMCPClient) TableName() string { return "oauth_server_mcp_clients" }

// A writer-locked per-issuer counter bounds unauthenticated registration even
// across processes. There is no unbounded in-memory cache or automatic eviction
// that could invalidate an already approved token family.
type OAuthServerMCPRegistry struct {
	Issuer string `gorm:"primaryKey;size:512"`
	Count  int    `gorm:"not null"`
}

func (OAuthServerMCPRegistry) TableName() string { return "oauth_server_mcp_registries" }
