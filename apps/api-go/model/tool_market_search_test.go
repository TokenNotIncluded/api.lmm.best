package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolMarketSearchAcceptsUnicodeCharacterLimit(t *testing.T) {
	f := newMarketFixture(t, 0)
	query := strings.Repeat("工", 120)
	require.NoError(t, f.db.Model(&ToolMarketVersion{}).Where("id = ?", f.tool.VersionID).Update("description", query).Error)
	rows, err := ListToolMarket(f.buyer.Id, "  "+query+"  ", "", 0, 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	for _, invalid := range []string{query + "工", string([]byte{0xff})} {
		_, err := ListToolMarket(f.buyer.Id, invalid, "", 0, 20)
		require.ErrorIs(t, err, ErrToolMarketInput)
	}
	require.NoError(t, f.db.Model(&ToolMarketVersion{}).Where("id = ?", f.tool.VersionID).Update("visibility", "private").Error)
	rows, err = ListToolMarket(f.buyer.Id, query, "", 0, 20)
	require.NoError(t, err)
	require.Empty(t, rows)
	rows, err = ListToolMarket(f.author.Id, query, "", 0, 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestToolMarketSearchKeepsWildcardCharactersLiteral(t *testing.T) {
	f := newMarketFixture(t, 0)
	for _, query := range []string{"%", "_", "!"} {
		rows, err := ListToolMarket(f.buyer.Id, query, "", 0, 20)
		require.NoError(t, err)
		require.Empty(t, rows)
	}
	require.NoError(t, f.db.Model(&ToolMarketVersion{}).Where("id = ?", f.tool.VersionID).Update("description", "100% tool_name!").Error)
	for _, query := range []string{"%", "_", "!"} {
		rows, err := ListToolMarket(f.buyer.Id, query, "", 0, 20)
		require.NoError(t, err)
		require.Len(t, rows, 1)
	}
}
