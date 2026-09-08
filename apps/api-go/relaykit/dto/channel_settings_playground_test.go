package dto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdvancedCustomPlaygroundRoutesPreserveEndpointAndModelRestrictions(t *testing.T) {
	for _, endpoint := range []string{"chat/completions", "images/generations", "images/edits"} {
		t.Run(endpoint, func(t *testing.T) {
			config := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{{
				IncomingPath: "/v1/" + endpoint,
				UpstreamPath: "/provider/" + endpoint,
				Models:       []string{"allowed-model"},
			}}}
			for _, prefix := range []string{"/v1/", "/pg/"} {
				path := prefix + endpoint
				require.True(t, config.SupportsPath(path), path)
				route, ok := config.MatchPathForModel(path, "allowed-model")
				require.True(t, ok, path)
				require.Equal(t, "/provider/"+endpoint, route.UpstreamPath)
				require.False(t, config.SupportsPathForModel(path, "other-model"), path)
			}
			require.False(t, config.SupportsPath("/pg/"+endpoint+"/extra"))
			require.False(t, config.SupportsPath("/pg/"+endpoint+"?group=image-2"))
		})
	}
	config := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{{IncomingPath: "/v1/images/variations"}}}
	require.False(t, config.SupportsPath("/pg/images/variations"), "unknown playground paths must not acquire relay aliases")
	config.Routes[0].IncomingPath = "/v1/images/generations"
	require.False(t, config.SupportsPath("/pg/images/edits"), "generation-only channels must not accept edits")
}

func TestAdvancedCustomPlaygroundPreservesExplicitRoutesAndOrder(t *testing.T) {
	for _, endpoint := range []string{"chat/completions", "images/generations", "images/edits"} {
		t.Run(endpoint, func(t *testing.T) {
			path := "/pg/" + endpoint
			explicit := AdvancedCustomRoute{IncomingPath: path, UpstreamPath: "/explicit", Models: []string{"allowed-model"}}
			alias := AdvancedCustomRoute{IncomingPath: "/v1/" + endpoint, UpstreamPath: "/alias", Models: []string{"allowed-model"}}
			config := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{explicit}}
			require.True(t, config.SupportsPath(path))
			require.False(t, config.SupportsPath(alias.IncomingPath), "explicit playground routes do not expose /v1 endpoints")
			for _, routes := range [][]AdvancedCustomRoute{{explicit}, {explicit, alias}, {alias, explicit}} {
				config.Routes = routes
				route, ok := config.MatchPathForModel(path, "allowed-model")
				require.True(t, ok)
				require.Equal(t, routes[0].UpstreamPath, route.UpstreamPath, "first matching configured route wins")
				require.False(t, config.SupportsPathForModel(path, "other-model"))
			}
		})
	}
}
