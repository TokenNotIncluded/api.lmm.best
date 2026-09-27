package authz

var AcquisitionRead = Permission{Resource: "acquisition", Action: ActionRead}
var AcquisitionWrite = Permission{Resource: "acquisition", Action: ActionWrite}
var AcquisitionDetails = Permission{Resource: "acquisition", Action: "details"}
var AcquisitionExport = Permission{Resource: "acquisition", Action: "export"}

func init() {
	RegisterResource(ResourceDefinition{Resource: "acquisition", LabelKey: "User acquisition", Actions: []ActionDefinition{
		{Action: ActionRead, LabelKey: "View acquisition summaries", DescriptionKey: "View aggregate channel and campaign results.", DefaultRoles: []string{BuiltInRoleAdmin}},
		{Action: ActionWrite, LabelKey: "Manage promotion links", DescriptionKey: "Create, archive, and delete promotion links.", DefaultRoles: []string{BuiltInRoleAdmin}},
		{Action: "details", LabelKey: "View acquisition account details", DescriptionKey: "View account-level source records."},
		{Action: "export", LabelKey: "Export acquisition details", DescriptionKey: "Export account-level source records."},
	}})
}
