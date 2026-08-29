package authz

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	"gorm.io/gorm"
)

var (
	enforcerMu sync.RWMutex
	enforcer   *casbin.SyncedEnforcer
)

const modelText = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act, eft

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = r.sub == p.sub && r.obj == p.obj && r.act == p.act && p.eft == "allow"
`

func Init(db *gorm.DB) error {
	return initEnforcer(db, true)
}

func InitForStartup(db *gorm.DB, applyMigrationData bool) error {
	return initEnforcer(db, applyMigrationData)
}

func initEnforcer(db *gorm.DB, applyMigrationData bool) error {
	if common.IsMasterNode && applyMigrationData {
		if err := seedBuiltInRoles(db); err != nil {
			return err
		}
		if err := resetBuiltInRolePolicies(db); err != nil {
			return err
		}
	} else if !applyMigrationData {
		if err := verifyBuiltInAuthorizationData(db); err != nil {
			return err
		}
	}

	m, err := casbinmodel.NewModelFromString(modelText)
	if err != nil {
		return err
	}
	e, err := casbin.NewSyncedEnforcer(m, newGormAdapter(db))
	if err != nil {
		return err
	}
	e.EnableAutoSave(true)

	enforcerMu.Lock()
	enforcer = e
	enforcerMu.Unlock()

	if !common.IsMasterNode || !applyMigrationData {
		return nil
	}
	return seedDefaultPolicies()
}

func verifyBuiltInAuthorizationData(db *gorm.DB) error {
	roleKeys := make([]string, 0, len(builtInRoles))
	expectedRoles := make(map[string]RoleSpec, len(builtInRoles))
	for _, spec := range builtInRoles {
		roleKeys = append(roleKeys, spec.Key)
		expectedRoles[spec.Key] = spec
	}
	var roles []model.AuthzRole
	if err := db.Where("key IN ?", roleKeys).Find(&roles).Error; err != nil {
		return fmt.Errorf("verify built-in authorization roles: %w", err)
	}
	if len(roles) != len(expectedRoles) {
		return fmt.Errorf("built-in authorization roles are incomplete: found %d, expected %d", len(roles), len(expectedRoles))
	}
	for _, role := range roles {
		spec, ok := expectedRoles[role.Key]
		if !ok || role.Name != spec.Name || role.Description != spec.Description ||
			role.BuiltIn != spec.BuiltIn || !role.Enabled || role.Sort != spec.Sort {
			return fmt.Errorf("built-in authorization role %q does not match its authoritative definition", role.Key)
		}
	}

	type policyKey struct {
		subject  string
		resource string
		action   string
		effect   string
	}
	expectedPolicies := make(map[policyKey]struct{})
	subjects := make([]string, 0, len(builtInRoles))
	for _, spec := range builtInRoles {
		subject := RoleSubject(spec.Key)
		subjects = append(subjects, subject)
		if spec.Superuser {
			continue
		}
		for _, permission := range PermissionsForRole(spec.Key) {
			expectedPolicies[policyKey{subject, permission.Resource, permission.Action, EffectAllow}] = struct{}{}
		}
	}
	var policies []model.CasbinRule
	if err := db.Where("ptype = ? AND v0 IN ?", "p", subjects).Find(&policies).Error; err != nil {
		return fmt.Errorf("verify built-in authorization policies: %w", err)
	}
	actualPolicies := make(map[policyKey]struct{}, len(policies))
	for _, policy := range policies {
		key := policyKey{policy.V0, policy.V1, policy.V2, policy.V3}
		if policy.Ptype != "p" || policy.V4 != "" || policy.V5 != "" {
			return fmt.Errorf("built-in authorization policy for %q has an invalid shape", policy.V0)
		}
		if _, ok := expectedPolicies[key]; !ok {
			return fmt.Errorf("unexpected built-in authorization policy %q/%q/%q/%q", key.subject, key.resource, key.action, key.effect)
		}
		actualPolicies[key] = struct{}{}
	}
	if len(actualPolicies) != len(expectedPolicies) {
		missing := make([]string, 0)
		for key := range expectedPolicies {
			if _, ok := actualPolicies[key]; !ok {
				missing = append(missing, fmt.Sprintf("%s/%s/%s/%s", key.subject, key.resource, key.action, key.effect))
			}
		}
		sort.Strings(missing)
		return fmt.Errorf("built-in authorization policies are incomplete: missing %s", missing[0])
	}
	return nil
}

func currentEnforcer() *casbin.SyncedEnforcer {
	enforcerMu.RLock()
	defer enforcerMu.RUnlock()
	return enforcer
}

func ReloadPolicy() error {
	enforcerMu.Lock()
	defer enforcerMu.Unlock()
	if enforcer == nil {
		return fmt.Errorf("authz enforcer is not initialized")
	}
	return enforcer.LoadPolicy()
}

// StartPolicySync periodically reloads the authorization policy from the database.
// The enforcer keeps an in-memory snapshot, and permission changes are written
// straight to the DB (see SetUserPermissionsInTx) with only the local node's
// snapshot refreshed afterwards. Without this loop other instances in a
// multi-node deployment would keep serving stale permissions (including not
// honoring a revoked grant) until restart. Mirrors model.SyncOptions polling.
func StartPolicySync(frequency int) {
	StartPolicySyncContext(context.Background(), frequency)
}

func StartPolicySyncContext(ctx context.Context, frequency int) {
	if frequency <= 0 {
		return
	}
	ticker := time.NewTicker(time.Duration(frequency) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := ReloadPolicy(); err != nil {
				common.SysError("failed to reload authz policy: " + err.Error())
			}
		}
	}
}
