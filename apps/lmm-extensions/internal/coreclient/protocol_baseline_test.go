package coreclient

import (
	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/protobuf/reflect/protoreflect"
	"testing"
)

// v1 additions are allowed. Reusing numbers or changing existing names, kinds,
// presence, cardinality or message targets requires a new protocol package.
func TestEventAndCommandV1FieldBaseline(t *testing.T) {
	specs := []struct {
		file               protoreflect.FileDescriptor
		message            string
		number             protoreflect.FieldNumber
		name, kind, target string
		repeated, optional bool
	}{
		{pb.File_lmm_core_v1_events_proto, "EventClientVersion", 1, "protocol_major", "uint32", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventClientVersion", 2, "protocol_minor", "uint32", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventClientVersion", 3, "required_features", "string", "", true, false},
		{pb.File_lmm_core_v1_events_proto, "NegotiateEventsRequest", 1, "consumer_id", "string", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "NegotiateEventsRequest", 2, "version", "message", "lmm.core.v1.EventClientVersion", false, false},
		{pb.File_lmm_core_v1_events_proto, "NegotiateEventsResponse", 1, "protocol_major", "uint32", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "NegotiateEventsResponse", 2, "protocol_minor", "uint32", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "NegotiateEventsResponse", 3, "features", "string", "", true, false},
		{pb.File_lmm_core_v1_events_proto, "NegotiateEventsResponse", 4, "max_batch", "uint32", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "NegotiateEventsResponse", 5, "max_payload_bytes", "uint32", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "NegotiateEventsResponse", 6, "lease_seconds", "uint32", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "NegotiateEventsResponse", 7, "event_types", "string", "", true, false},
		{pb.File_lmm_core_v1_events_proto, "EventEnvelope", 1, "event_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventEnvelope", 2, "event_key", "string", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventEnvelope", 3, "event_type", "string", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventEnvelope", 4, "schema_version", "uint32", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventEnvelope", 5, "resource_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventEnvelope", 6, "resource_version", "int64", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventEnvelope", 7, "payload", "bytes", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventEnvelope", 8, "content_sha256", "bytes", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventDelivery", 1, "event", "message", "lmm.core.v1.EventEnvelope", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventDelivery", 2, "lease_token", "bytes", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventDelivery", 3, "attempt", "uint32", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "EventDelivery", 4, "lease_until_unix_ms", "int64", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "PullEventsRequest", 1, "consumer_id", "string", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "PullEventsRequest", 2, "version", "message", "lmm.core.v1.EventClientVersion", false, false},
		{pb.File_lmm_core_v1_events_proto, "PullEventsRequest", 3, "max_events", "uint32", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "PullEventsResponse", 1, "deliveries", "message", "lmm.core.v1.EventDelivery", true, false},
		{pb.File_lmm_core_v1_events_proto, "AcknowledgeEventRequest", 1, "consumer_id", "string", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "AcknowledgeEventRequest", 2, "event_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "AcknowledgeEventRequest", 3, "lease_token", "bytes", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "AcknowledgeEventResponse", 1, "already_acknowledged", "bool", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "RetryEventRequest", 1, "consumer_id", "string", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "RetryEventRequest", 2, "event_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "RetryEventRequest", 3, "lease_token", "bytes", "", false, false},
		{pb.File_lmm_core_v1_events_proto, "RetryEventRequest", 4, "reason", "enum", "lmm.core.v1.EventRetryReason", false, false},
		{pb.File_lmm_core_v1_events_proto, "RetryEventResponse", 1, "already_acknowledged", "bool", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "MutationContext", 1, "idempotency_key", "string", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "MutationContext", 2, "expected_version", "int64", "", false, true},
		{pb.File_lmm_core_v1_commands_proto, "MutationReceipt", 1, "operation_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "MutationReceipt", 2, "resource_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "MutationReceipt", 3, "resource_version", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "MutationReceipt", 4, "event_ids", "int64", "", true, false},
		{pb.File_lmm_core_v1_commands_proto, "LedgerAmount", 1, "units", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "LedgerAmount", 2, "asset", "string", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "RevokeCredentialRequest", 1, "mutation", "message", "lmm.core.v1.MutationContext", false, false},
		{pb.File_lmm_core_v1_commands_proto, "RevokeCredentialRequest", 2, "credential_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "CaptureVerifiedPaymentRequest", 1, "mutation", "message", "lmm.core.v1.MutationContext", false, false},
		{pb.File_lmm_core_v1_commands_proto, "CaptureVerifiedPaymentRequest", 2, "verified_payment_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "TransferFundsRequest", 1, "mutation", "message", "lmm.core.v1.MutationContext", false, false},
		{pb.File_lmm_core_v1_commands_proto, "TransferFundsRequest", 2, "source", "message", "lmm.core.v1.Account", false, false},
		{pb.File_lmm_core_v1_commands_proto, "TransferFundsRequest", 3, "destination", "message", "lmm.core.v1.Account", false, false},
		{pb.File_lmm_core_v1_commands_proto, "TransferFundsRequest", 4, "amount", "message", "lmm.core.v1.LedgerAmount", false, false},
		{pb.File_lmm_core_v1_commands_proto, "RefundFundsRequest", 1, "mutation", "message", "lmm.core.v1.MutationContext", false, false},
		{pb.File_lmm_core_v1_commands_proto, "RefundFundsRequest", 2, "original_operation_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "RefundFundsRequest", 3, "amount", "message", "lmm.core.v1.LedgerAmount", false, false},
		{pb.File_lmm_core_v1_commands_proto, "ReserveUsageRequest", 1, "mutation", "message", "lmm.core.v1.MutationContext", false, false},
		{pb.File_lmm_core_v1_commands_proto, "ReserveUsageRequest", 2, "quote_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "ReserveUsageRequest", 3, "model_request_id", "string", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "ReservationReceipt", 1, "mutation", "message", "lmm.core.v1.MutationReceipt", false, false},
		{pb.File_lmm_core_v1_commands_proto, "ReservationReceipt", 2, "reservation_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "ReservationReceipt", 3, "charged_account", "message", "lmm.core.v1.Account", false, false},
		{pb.File_lmm_core_v1_commands_proto, "UsageCounters", 1, "input_tokens", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "UsageCounters", 2, "output_tokens", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "UsageCounters", 3, "cache_read_tokens", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "UsageCounters", 4, "cache_write_tokens", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "SettleUsageRequest", 1, "mutation", "message", "lmm.core.v1.MutationContext", false, false},
		{pb.File_lmm_core_v1_commands_proto, "SettleUsageRequest", 2, "reservation_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "SettleUsageRequest", 3, "verified_usage_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "ReleaseUsageRequest", 1, "mutation", "message", "lmm.core.v1.MutationContext", false, false},
		{pb.File_lmm_core_v1_commands_proto, "ReleaseUsageRequest", 2, "reservation_id", "int64", "", false, false},
		{pb.File_lmm_core_v1_commands_proto, "ActivateConfigurationRequest", 1, "mutation", "message", "lmm.core.v1.MutationContext", false, false},
		{pb.File_lmm_core_v1_commands_proto, "ActivateConfigurationRequest", 2, "validated_configuration_id", "int64", "", false, false},
	}
	for _, s := range specs {
		m := s.file.Messages().ByName(protoreflect.Name(s.message))
		if m == nil {
			t.Fatalf("missing message %s", s.message)
		}
		f := m.Fields().ByNumber(s.number)
		if f == nil || string(f.Name()) != s.name || f.Kind().String() != s.kind || f.IsList() != s.repeated || f.IsMap() || f.HasOptionalKeyword() != s.optional {
			t.Fatalf("changed %s field %d", s.message, s.number)
		}
		if f.Message() != nil && string(f.Message().FullName()) != s.target {
			t.Fatalf("changed message target %s.%s", s.message, s.name)
		}
		if f.Enum() != nil && string(f.Enum().FullName()) != s.target {
			t.Fatalf("changed enum target %s.%s", s.message, s.name)
		}
		if f.ContainingOneof() != nil && !f.ContainingOneof().IsSynthetic() {
			t.Fatalf("moved field into oneof %s.%s", s.message, s.name)
		}
	}
}
func TestEventV1MethodsAndEnums(t *testing.T) {
	file := pb.File_lmm_core_v1_events_proto
	service := file.Services().ByName("CoreEvents")
	if service == nil || file.Package() != "lmm.core.v1" {
		t.Fatal("missing v1 service")
	}
	for _, s := range []struct{ name, in, out string }{
		{"Negotiate", "NegotiateEventsRequest", "NegotiateEventsResponse"},
		{"Pull", "PullEventsRequest", "PullEventsResponse"},
		{"Acknowledge", "AcknowledgeEventRequest", "AcknowledgeEventResponse"},
		{"Retry", "RetryEventRequest", "RetryEventResponse"},
	} {
		m := service.Methods().ByName(protoreflect.Name(s.name))
		if m == nil || m.IsStreamingClient() || m.IsStreamingServer() || string(m.Input().Name()) != s.in || string(m.Output().Name()) != s.out {
			t.Fatalf("changed %s", s.name)
		}
	}
	e := file.Enums().ByName("EventRetryReason")
	for n, want := range []string{"EVENT_RETRY_REASON_UNSPECIFIED", "EVENT_RETRY_REASON_HANDLER_FAILURE", "EVENT_RETRY_REASON_UNSUPPORTED_EVENT", "EVENT_RETRY_REASON_DEPENDENCY_UNAVAILABLE"} {
		if e == nil || e.Values().ByNumber(protoreflect.EnumNumber(n)) == nil || string(e.Values().ByNumber(protoreflect.EnumNumber(n)).Name()) != want {
			t.Fatal("changed retry reason")
		}
	}
}
