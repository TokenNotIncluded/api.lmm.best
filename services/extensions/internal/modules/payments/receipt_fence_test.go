package payments

import "testing"

func TestPreCreditExternalRefundFenceNeedsNoInventedMoneyJournal(t *testing.T) {
	receipt := Receipt{ID: "pay_durable_fence", IntentID: 1, Key: "fence_00000000001", RequestHash: make([]byte, 32), State: ExternalRefundApplied}
	if !receiptMatches(receipt, 1, receipt.Key, ExternalRefundApplied) {
		t.Fatal("durable zero-money fence rejected")
	}
	if receiptMatches(receipt, 1, receipt.Key, CreditApplied) {
		t.Fatal("refund fence became top-up success")
	}
	receipt.State = CreditApplied
	if receiptMatches(receipt, 1, receipt.Key, CreditApplied) {
		t.Fatal("zero journal invented credit")
	}
	receipt.State = ExternalRefundApplied
	receipt.JournalID = -1
	if receiptMatches(receipt, 1, receipt.Key, ExternalRefundApplied) {
		t.Fatal("negative journal accepted")
	}
}
