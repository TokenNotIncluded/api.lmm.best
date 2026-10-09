package model

// These are the PostgreSQL 17 catalog renderings of the portable GORM checks.
// They are checked against real migrated tables, and their Source must still
// match the model tag. The common verifier remains exact: no casts, columns,
// enum values or boolean terms are discarded to accept a mismatched schema.
func (NativeAccountRecord) postgresCheckCatalogForms() map[string]postgresCheckCatalogForm {
	return map[string]postgresCheckCatalogForm{
		"ck_accounts_identity": {
			Source:     "id > 0 AND (kind <> 'personal' OR id = owner_user_id)",
			Expression: "id > 0 AND (kind::text <> 'personal'::text OR id = owner_user_id)",
			Columns:    []string{"id", "kind", "owner_user_id"},
		},
		"ck_accounts_kind": {
			Source:     "kind IN ('personal','team')",
			Expression: "kind::text = ANY (ARRAY['personal'::character varying, 'team'::character varying]::text[])",
			Columns:    []string{"kind"},
		},
	}
}

func (NativeAccountMember) postgresCheckCatalogForms() map[string]postgresCheckCatalogForm {
	return map[string]postgresCheckCatalogForm{
		"ck_account_members_kind": nativeTeamKindCheckCatalogForm(),
		"ck_account_members_role": nativeTeamRoleCheckCatalogForm(),
	}
}

func (NativeAccountInvitation) postgresCheckCatalogForms() map[string]postgresCheckCatalogForm {
	return map[string]postgresCheckCatalogForm{
		"ck_account_invites_kind": nativeTeamKindCheckCatalogForm(),
		"ck_account_invites_role": nativeTeamRoleCheckCatalogForm(),
		"ck_account_invites_status": {
			Source:     "status IN ('pending','accepted','revoked')",
			Expression: "status::text = ANY (ARRAY['pending'::character varying, 'accepted'::character varying, 'revoked'::character varying]::text[])",
			Columns:    []string{"status"},
		},
	}
}

func nativeTeamKindCheckCatalogForm() postgresCheckCatalogForm {
	return postgresCheckCatalogForm{
		Source: "account_kind = 'team'", Expression: "account_kind::text = 'team'::text", Columns: []string{"account_kind"},
	}
}

func nativeTeamRoleCheckCatalogForm() postgresCheckCatalogForm {
	return postgresCheckCatalogForm{
		Source:     "role IN ('admin','member')",
		Expression: "role::text = ANY (ARRAY['admin'::character varying, 'member'::character varying]::text[])",
		Columns:    []string{"role"},
	}
}
