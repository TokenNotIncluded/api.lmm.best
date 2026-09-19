package controller

import (
	"bytes"
	"encoding/csv"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAcquisitionCSVProtectsSpreadsheetFormulaAndContainsNoContactFields(t *testing.T) {
	values := []string{"=CMD()", " +cmd", "\t@SUM(1)", "\uFEFF-cmd", "\u200B=cmd"}
	for _, value := range values {
		require.Equal(t, "'"+value, acquisitionCSVCell(value))
	}
	data, err := acquisitionAccountCSV([]model.AcquisitionExportRow{{UserID: 4, ObservedSource: "=1+1", CorrectedSource: "forum, docs\nnext", RegisteredAt: 10}}, "@filter", 1, 100)
	require.NoError(t, err)
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.Equal(t, "'=1+1", records[1][2])
	require.Equal(t, "'@filter", records[1][7])
	require.Equal(t, "forum, docs\nnext", records[1][4])
	require.NotContains(t, string(data), "email")
	require.NotContains(t, string(data), "username")
}
