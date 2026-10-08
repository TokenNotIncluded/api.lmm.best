// Package deploymentfence contains only the shared deployment/activation
// coordination contract. It neither opens a database nor mutates model state.
package deploymentfence

import "strings"

// AdvisoryKey is independent of startup migration's ...0001 advisory key.
// Deployment holds shared, activation holds exclusive, always before taking
// the existing migration lock. Never hold the migration lock across VERIFY.
const AdvisoryKey int64 = 0x4c4d4d4150490002

// OptionPrefix marks durable ACTIVE owners in the existing options table.
// Every matching row, including unknown or malformed values, blocks activation
// until its exact sealed owner completes an explicitly reviewed CAS cleanup.
const OptionPrefix = "MerchantStoreDeploymentFence:"

func ReservedOptionKey(key string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), strings.ToLower(OptionPrefix))
}

// PostgreSQLPresencePredicate uses the same Unicode whitespace set as Go's
// strings.TrimSpace, including abnormal whitespace/case records.
// Values are intentionally not interpreted: absence alone can permit activation.
const PostgreSQLPresencePredicate = `pg_catalog.lower(pg_catalog.btrim(key, E' \t\n\r\f\013\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000')) LIKE 'merchantstoredeploymentfence:%'`
