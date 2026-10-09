package corepb

import (
    "strings"
    "testing"
    "google.golang.org/protobuf/reflect/protoreflect"
)

// This v1 baseline permits additions, not changed field numbers/types/names.
// New mandatory authorization semantics require explicit protocol negotiation.
func TestV1FieldsAndMethodsRemainCompatible(t *testing.T) {
    file := File_lmm_core_v1_control_proto
    if file.Package() != "lmm.core.v1" { t.Fatal("v1 package changed") }
    fields := map[string][]string{
        "CapabilitiesRequest": {},
        "CapabilitiesResponse": {"protocol_major:uint32", "identity_available:bool", "features:string:repeated"},
        "AuthorizeRequest": {},
        "Account": {"kind:enum:AccountKind", "id:int64"},
        "AuthorizeResponse": {"credential_id:int64", "credential_kind:enum:CredentialKind", "user_id:int64", "platform_level:int32", "owner:message:Account", "funding_accounts:message:Account:repeated"},
        "ListTeamsRequest": {"after_id:int64"},
        "Team": {"id:int64", "role:enum:TeamRole", "membership_version:int64", "can_spend:bool"},
        "ListTeamsResponse": {"teams:message:Team:repeated", "next_after_id:int64"},
    }
    for name, expected := range fields {
        message := file.Messages().ByName(protoreflect.Name(name))
        if message == nil { t.Fatalf("missing v1 message %s", name) }
        for index, spec := range expected {
            field := message.Fields().ByNumber(protoreflect.FieldNumber(index+1))
            if field == nil { t.Fatalf("%s: missing field %d",name,index+1) }
            parts := []string{string(field.Name()),field.Kind().String()}
            if field.Kind()==protoreflect.EnumKind { parts=append(parts,string(field.Enum().Name())) }
            if field.Kind()==protoreflect.MessageKind { parts=append(parts,string(field.Message().Name())) }
            if field.IsList() { parts=append(parts,"repeated") }
            if field.IsMap() || field.ContainingOneof()!=nil || field.HasOptionalKeyword() || strings.Join(parts,":")!=spec {
                t.Fatalf("%s field %d changed: %v != %s",name,index+1,parts,spec)
            }
        }
    }
    enums := map[string][]string{
        "AccountKind": {"ACCOUNT_KIND_UNSPECIFIED","ACCOUNT_KIND_PERSONAL","ACCOUNT_KIND_TEAM"},
        "CredentialKind": {"CREDENTIAL_KIND_UNSPECIFIED","CREDENTIAL_KIND_SESSION","CREDENTIAL_KIND_API_KEY"},
        "TeamRole": {"TEAM_ROLE_UNSPECIFIED","TEAM_ROLE_OWNER","TEAM_ROLE_ADMIN","TEAM_ROLE_MEMBER"},
    }
    for name, values := range enums {
        enum := file.Enums().ByName(protoreflect.Name(name))
        if enum==nil { t.Fatalf("missing enum %s",name) }
        for index, name := range values {
            value := enum.Values().ByNumber(protoreflect.EnumNumber(index))
            if value==nil || string(value.Name())!=name { t.Fatalf("enum %s changed value %d",enum.Name(),index) }
        }
    }
    service:=file.Services().ByName("CoreControl")
    if service==nil { t.Fatal("missing v1 service") }
    for _, name:= range []protoreflect.Name{"Capabilities","Authorize","ListTeams"} {
        method:=service.Methods().ByName(name)
        if method==nil || method.IsStreamingClient() || method.IsStreamingServer() ||
            string(method.Input().FullName())!="lmm.core.v1."+string(name)+"Request" ||
            string(method.Output().FullName())!="lmm.core.v1."+string(name)+"Response" {
            t.Fatalf("v1 method %s changed",name)
        }
    }
}
