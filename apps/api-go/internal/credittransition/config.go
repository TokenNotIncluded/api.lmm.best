// Package credittransition implements the sealed, non-business preparation
// contract. It never installs a legacy currency basis into the application.
package credittransition

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
)

const (
	Format            = "lmm-credit-transition-prepare-v1"
	PlanEnvironment   = "LMM_CREDIT_TRANSITION_PLAN"
	DigestEnvironment = "LMM_CREDIT_TRANSITION_SHA256"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var safeSchema = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

var OptionKeys = []string{"CreditsPerUSD", "LegacyPricingQuotaPerUnit", "QuotaPerUnit", "PublicCreditsPerUSD", "USDExchangeRate"}

type DatabaseIdentity struct {
	SystemIdentifier string `json:"system_identifier"`
	Database         string `json:"database"`
	DatabaseOID      uint64 `json:"database_oid" gorm:"column:database_oid"`
	Schema           string `json:"schema"`
	ServerVersionNum int    `json:"server_version_num"`
	DatabaseUser     string `json:"database_user"`
}

type Config struct {
	Format                 string            `json:"format"`
	TransitionID           string            `json:"transition_id"`
	TransitionIntentSHA256 string            `json:"transition_intent_sha256"`
	ProviderSHA256         string            `json:"provider_sha256"`
	TargetCreditsPerUSD    int64             `json:"target_credits_per_usd"`
	Database               DatabaseIdentity  `json:"database"`
	Options                map[string]string `json:"options"`
}

func Requested() bool {
	return os.Getenv(PlanEnvironment) != "" || os.Getenv(DigestEnvironment) != ""
}

func IsSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}

func (config Config) Validate() error {
	if config.Format != Format || !safeID.MatchString(config.TransitionID) ||
		!IsSHA256(config.TransitionIntentSHA256) || !IsSHA256(config.ProviderSHA256) ||
		config.TargetCreditsPerUSD != common.FixedCreditsPerUSD {
		return errors.New("invalid sealed credit transition identity or target")
	}
	db := config.Database
	systemID, systemErr := strconv.ParseUint(db.SystemIdentifier, 10, 64)
	if systemErr != nil || systemID == 0 ||
		db.Database == "" || len(db.Database) > 63 || strings.ContainsRune(db.Database, '\x00') ||
		db.DatabaseOID == 0 || db.DatabaseOID > 4294967295 || !safeSchema.MatchString(db.Schema) || strings.HasPrefix(db.Schema, "pg_") || db.Schema == "information_schema" ||
		db.ServerVersionNum < 100000 || db.DatabaseUser == "" || len(db.DatabaseUser) > 63 || strings.ContainsRune(db.DatabaseUser, '\x00') {
		return errors.New("incomplete sealed PostgreSQL identity")
	}
	if len(config.Options) != len(OptionKeys) {
		return errors.New("sealed credit transition must include exactly all four anchors and fiat FX")
	}
	legacy := false
	for _, key := range OptionKeys {
		value, exists := config.Options[key]
		if !exists || len(value) == 0 || len(value) > 80 {
			return fmt.Errorf("missing or invalid sealed option %s", key)
		}
		rate, err := decimal.NewFromString(value)
		if err != nil || !rate.IsPositive() || rate.Exponent() < -18 || rate.Exponent() > 18 || rate.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
			return fmt.Errorf("invalid sealed option %s", key)
		}
		if key != "USDExchangeRate" && !rate.Equal(decimal.NewFromInt(common.FixedCreditsPerUSD)) {
			legacy = true
		}
	}
	if !legacy {
		return errors.New("preparation is forbidden after all credit anchors are canonical")
	}
	return nil
}

func Decode(data []byte) (Config, error) {
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode sealed credit transition: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, errors.New("sealed credit transition has trailing content")
	}
	return config, config.Validate()
}

// ReadConfig requires an immutable root-controlled path and an independently
// supplied byte digest; a mutable environment value alone cannot authorize it.
func ReadConfig(path, expectedSHA256 string) (Config, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !IsSHA256(expectedSHA256) {
		return Config{}, errors.New("credit preparation requires an absolute sealed plan and its SHA-256")
	}
	data, err := readSealedFile(path)
	if err != nil {
		return Config{}, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != expectedSHA256 {
		return Config{}, errors.New("sealed credit transition SHA-256 mismatch")
	}
	return Decode(data)
}

func (config Config) VerifyProvider(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("cannot read preparation provider")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("preparation provider is not a regular binary")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil || hex.EncodeToString(hash.Sum(nil)) != config.ProviderSHA256 {
		return errors.New("preparation provider SHA-256 mismatch")
	}
	return nil
}

func MaintenanceBody(id string) string { return "lmm-credit-transition:" + id }
