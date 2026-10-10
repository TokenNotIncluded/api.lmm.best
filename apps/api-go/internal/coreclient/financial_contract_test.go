package coreclient

import (
	"testing"

	pb "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/corepb"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// These are wire-contract tests, not money movement or payment acceptance.
func TestLedgerContractActionsAndOutcomesAreStable(t *testing.T) {
	for _, spec := range []struct {
		message        protoreflect.MessageDescriptor
		oneof          string
		names, targets []string
	}{
		{(&pb.PostLedgerRequest{}).ProtoReflect().Descriptor(), "action",
			[]string{"credit", "transfer", "charge", "reserve", "capture", "release", "refund", "reverse"},
			[]string{"LedgerCredit", "LedgerTransfer", "LedgerCharge", "LedgerReserve", "LedgerCapture", "LedgerRelease", "LedgerRefund", "LedgerReverse"}},
		{(&pb.PostLedgerResponse{}).ProtoReflect().Descriptor(), "outcome",
			[]string{"posted", "rejected"}, []string{"LedgerPosted", "LedgerRejected"}},
	} {
		for i, name := range spec.names {
			f := spec.message.Fields().ByNumber(protoreflect.FieldNumber(10 + i))
			if f == nil || string(f.Name()) != name || f.Kind() != protoreflect.MessageKind || f.IsList() ||
				f.ContainingOneof() == nil || string(f.ContainingOneof().Name()) != spec.oneof ||
				string(f.Message().FullName()) != "lmm.core.v1."+spec.targets[i] {
				t.Fatalf("changed %s action/outcome %s", spec.message.FullName(), name)
			}
		}
	}
}

func TestFinancialContractIntegerFieldsAndMethods(t *testing.T) {
	for _, s := range []struct {
		message proto.Message
		field   protoreflect.FieldNumber
		name    string
	}{
		{&pb.LedgerTransfer{}, 1, "source_account_id"},
		{&pb.LedgerTransfer{}, 2, "destination_account_id"},
		{&pb.LedgerTransfer{}, 3, "amount"},
		{&pb.LedgerCapture{}, 1, "reservation_journal_id"},
		{&pb.LedgerCapture{}, 2, "verified_usage_id"},
		{&pb.LedgerRefund{}, 1, "original_journal_id"},
		{&pb.LedgerRefund{}, 2, "amount"},
		{&pb.LedgerEntrySnapshot{}, 1, "ledger_account_id"},
		{&pb.LedgerEntrySnapshot{}, 2, "owner_account_id"},
		{&pb.LedgerEntrySnapshot{}, 3, "delta"},
		{&pb.LedgerEntrySnapshot{}, 4, "resulting_balance"},
		{&pb.LedgerEntrySnapshot{}, 5, "revision"},
		{&pb.PrepareTopupRequest{}, 2, "account_id"},
		{&pb.PrepareTopupRequest{}, 3, "payment_channel_id"},
		{&pb.PrepareTopupRequest{}, 5, "amount_minor"},
		{&pb.PaymentIntent{}, 1, "intent_id"},
		{&pb.PaymentIntent{}, 2, "account_id"},
		{&pb.PaymentIntent{}, 3, "created_by_user_id"},
		{&pb.PaymentIntent{}, 8, "amount_minor"},
		{&pb.PaymentIntent{}, 9, "credit_units"},
		{&pb.HoldRefundRequest{}, 2, "intent_id"},
		{&pb.HoldRefundRequest{}, 3, "refund_amount_minor"},
		{&pb.PaymentReceipt{}, 2, "intent_id"},
		{&pb.PaymentReceipt{}, 6, "journal_id"},
	} {
		f := s.message.ProtoReflect().Descriptor().Fields().ByNumber(s.field)
		if f == nil || string(f.Name()) != s.name || f.Kind() != protoreflect.Int64Kind || f.IsList() || f.ContainingOneof() != nil {
			t.Fatalf("changed %T.%s", s.message, s.name)
		}
	}
	for _, s := range []struct {
		file                           protoreflect.FileDescriptor
		service, method, input, output string
	}{
		{pb.File_lmm_core_v1_ledger_proto, "CoreLedger", "PostLedger", "PostLedgerRequest", "PostLedgerResponse"},
		{pb.File_lmm_core_v1_ledger_proto, "CoreLedger", "GetLedgerBalance", "GetLedgerBalanceRequest", "GetLedgerBalanceResponse"},
		{pb.File_lmm_core_v1_payments_proto, "CorePayments", "PrepareTopup", "PrepareTopupRequest", "PaymentIntent"},
		{pb.File_lmm_core_v1_payments_proto, "CorePayments", "CreditTopup", "CreditTopupRequest", "PaymentReceipt"},
		{pb.File_lmm_core_v1_payments_proto, "CorePayments", "HoldRefund", "HoldRefundRequest", "PaymentReceipt"},
		{pb.File_lmm_core_v1_payments_proto, "CorePayments", "CommitRefund", "ResolveRefundRequest", "PaymentReceipt"},
		{pb.File_lmm_core_v1_payments_proto, "CorePayments", "ReleaseRefund", "ResolveRefundRequest", "PaymentReceipt"},
		{pb.File_lmm_core_v1_payments_proto, "CorePayments", "ApplyExternalRefund", "ExternalRefundRequest", "PaymentReceipt"},
		{pb.File_lmm_core_v1_payments_proto, "CorePayments", "GetPaymentReceipt", "GetPaymentReceiptRequest", "PaymentReceipt"},
	} {
		service := s.file.Services().ByName(protoreflect.Name(s.service))
		if service == nil {
			t.Fatalf("missing service %s", s.service)
		}
		method := service.Methods().ByName(protoreflect.Name(s.method))
		if method == nil || method.IsStreamingClient() || method.IsStreamingServer() ||
			string(method.Input().FullName()) != "lmm.core.v1."+s.input || string(method.Output().FullName()) != "lmm.core.v1."+s.output {
			t.Fatalf("changed %s.%s", s.service, s.method)
		}
	}
	values := pb.File_lmm_core_v1_payments_proto.Enums().ByName("PaymentReceiptState").Values()
	for n, name := range []string{"UNSPECIFIED", "CREDIT_APPLIED", "REFUND_HELD", "REFUND_COMMITTED", "REFUND_RELEASED", "EXTERNAL_REFUND_APPLIED", "REJECTED"} {
		v := values.ByNumber(protoreflect.EnumNumber(n))
		if v == nil || string(v.Name()) != "PAYMENT_RECEIPT_STATE_"+name {
			t.Fatalf("changed receipt state %d", n)
		}
	}
}

func TestFinancialContractLargeIntegersAndUnknownAction(t *testing.T) {
	const large = int64(9007199254740993)
	want := &pb.PostLedgerRequest{ProtocolMajor: 1, Unit: "credit_500k_usd",
		Action: &pb.PostLedgerRequest_Transfer{Transfer: &pb.LedgerTransfer{
			SourceAccountId: large, DestinationAccountId: 9223372036854775807, Amount: large,
		}},
	}
	wire, err := proto.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got pb.PostLedgerRequest
	if err = proto.Unmarshal(wire, &got); err != nil || !proto.Equal(want, &got) {
		t.Fatal("integer precision loss", err)
	}
	unknown := protowire.AppendTag(nil, 99, protowire.BytesType)
	unknown = protowire.AppendBytes(unknown, []byte{8, 1})
	var future pb.PostLedgerRequest
	if err = proto.Unmarshal(unknown, &future); err != nil {
		t.Fatal(err)
	}
	if future.Action != nil || len(future.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("unknown action became an executable action")
	}
	// A future dispatcher must reject this missing known action, not default it
	// to credit. No dispatcher is registered by this contract-only task.
}
