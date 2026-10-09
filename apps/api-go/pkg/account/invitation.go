package account

// CanInviteRole checks the role being granted, not just access to the management
// page. Ownership transfer is a separate operation and never an invitation.
func (s Scope) CanInviteRole(role TeamRole) bool {
	if role != Admin && role != Member {
		return false
	}
	return s.role == Owner || s.role == Admin && role == Member
}
