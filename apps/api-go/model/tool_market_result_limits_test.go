package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolMarketResultRetainsDefaultLimitForRemoteAndOtherBuiltins(t *testing.T) {
	data := json.RawMessage(`{"text":"` + strings.Repeat("x", ToolMarketResultMaxBytes) + `"}`)
	for _, fixture := range []func(*testing.T) marketFixture{
		func(t *testing.T) marketFixture { return newMarketFixture(t, 100) },
		func(t *testing.T) marketFixture { return newMarketBuiltinFixture(t, 1) },
	} {
		f := fixture(t)
		in := f.input("oversized-result")
		if f.service.OwnerID == 0 {
			in = marketTestBuiltinInput(f, "oversized-result")
		}
		call, _, err := ReserveToolMarketCall(in)
		require.NoError(t, err)
		_, err = StartToolMarketCall(call.ID)
		require.NoError(t, err)
		require.ErrorIs(t, RecordToolMarketResult(call.ID, true, data), ErrToolMarketInput)
		require.ErrorIs(t, PrepareToolMarketBuiltinDrawingResult(call.ID, data), ErrToolMarketDenied, "the larger image limit requires the exact code-owned drawing service and tool")
	}
}

func TestToolMarketDeliveryDataUsesSufficientDialectCapacity(t *testing.T) {
	marketTestDB(t)
	for _, dialect := range []string{"mysql", "postgres", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			db := subscriptionResetDialectDB(t, dialect)
			want := "TEXT"
			if dialect == "mysql" {
				want = "LONGTEXT"
			}
			require.Equal(t, want, ToolMarketDeliveryData("").GormDBDataType(db, nil))
			for _, value := range []any{&ToolMarketResult{}, &ToolMarketBuiltinContinuation{}} {
				statement := db.Statement
				require.NoError(t, statement.Parse(value))
				field := statement.Schema.LookUpField("Data")
				require.Equal(t, want, db.Migrator().FullDataTypeOf(field).SQL, "the actual migration must use the delivery type rather than a 64 KiB MySQL TEXT override")
			}
		})
	}
}
