// Pure offline authority enrichment. Run from apps/api-go with `go run` and
// explicit input/output paths. No database initialization or connection exists.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

func main() {
	input := flag.String("input", "", "private frozen snapshot")
	output := flag.String("output", "", "private enriched snapshot")
	flag.Parse()
	if *input == "" || *output == "" || *input == *output {
		fail("distinct explicit private input/output paths required")
	}
	content, err := os.ReadFile(*input)
	if err != nil {
		fail("cannot read private snapshot")
	}
	var snapshot map[string]json.RawMessage
	if json.Unmarshal(content, &snapshot) != nil {
		fail("invalid private snapshot JSON")
	}
	var options map[string]string
	if json.Unmarshal(snapshot["options"], &options) != nil {
		fail("exact frozen options required")
	}
	quota, err := strconv.ParseFloat(options["QuotaPerUnit"], 64)
	if err != nil || quota <= 0 || math.IsNaN(quota) || math.IsInf(quota, 0) {
		fail("invalid frozen normalization quota unit")
	}
	common.QuotaPerUnit = quota
	for _, key := range []string{"topups", "pending_topups"} {
		var rows []map[string]json.RawMessage
		if json.Unmarshal(snapshot[key], &rows) != nil || rows == nil {
			fail("complete topup arrays required")
		}
		for _, row := range rows {
			var money string
			if json.Unmarshal(row["money"], &money) != nil {
				fail("exact raw database money text required")
			}
			originalMoney := row["money"]
			row["money"] = json.RawMessage(money)
			encoded, err := json.Marshal(row)
			if err != nil {
				fail("invalid topup projection")
			}
			var topup model.TopUp
			if json.Unmarshal(encoded, &topup) != nil {
				fail("invalid raw topup facts")
			}
			row["money"] = originalMoney
			facts := model.ExportWalletTopUpCreditRebaseFacts(&topup)
			row["effective_credited_quota"], _ = json.Marshal(facts.EffectiveCreditedQuota)
			row["paid_amount_micros"], _ = json.Marshal(facts.PaidAmountMicros)
			row["is_legacy_linuxdo_credit_topup"], _ = json.Marshal(facts.IsLegacyLinuxDOCreditTopUp)
		}
		snapshot[key], err = json.Marshal(rows)
		if err != nil {
			fail("cannot encode private facts")
		}
	}
	enriched, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		fail("cannot encode private snapshot")
	}
	file, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fail("private output must be a new file")
	}
	defer file.Close()
	if _, err = file.Write(enriched); err != nil {
		fail("private snapshot write failed")
	}
	if file.Sync() != nil {
		fail("private snapshot sync failed")
	}
}

func fail(message string) { fmt.Fprintln(os.Stderr, "error:", message); os.Exit(2) }
