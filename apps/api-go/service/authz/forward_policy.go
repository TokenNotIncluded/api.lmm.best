package authz

import (
	"regexp"

	"github.com/LIghtJUNction/api.lmm.best/model"
)

var forwardResourceName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// This compatibility boundary is intentionally narrower than an arbitrary
// unknown permission: only a new resource's admin read/write defaults can be
// retained during N-1 verification. Known-resource grants, root policy rows,
// denials, wildcards, and malformed shapes remain verification errors.
// Can rejects unknown permissions for non-superusers before consulting policy;
// retaining these rows therefore cannot grant an action in the older runtime.
func isFutureBuiltInReadWritePolicy(policy model.CasbinRule) bool {
	return policy.Ptype == "p" && policy.V0 == RoleSubject(BuiltInRoleAdmin) &&
		forwardResourceName.MatchString(policy.V1) && !isKnownResource(policy.V1) &&
		(policy.V2 == ActionRead || policy.V2 == ActionWrite) &&
		policy.V3 == EffectAllow && policy.V4 == "" && policy.V5 == ""
}
