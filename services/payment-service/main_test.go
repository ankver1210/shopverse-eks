package main

import (
	"strings"
	"testing"
)

func TestDecide(t *testing.T) {
	cases := []struct {
		name   string
		req    chargeRequest
		reason string
	}{
		{"approved", chargeRequest{AmountCents: 1000, PaymentMethod: "tok_visa"}, ""},
		{"declined card", chargeRequest{AmountCents: 1000, PaymentMethod: "tok_declined"}, "declined"},
		{"fraud limit", chargeRequest{AmountCents: MaxAmountCents + 1, PaymentMethod: "tok_visa"}, "fraud"},
		{"unknown method", chargeRequest{AmountCents: 1000, PaymentMethod: "tok_bitcoin"}, "unsupported"},
	}
	for _, c := range cases {
		got := decide(c.req)
		if (c.reason == "" && got != "") || (c.reason != "" && !strings.Contains(got, c.reason)) {
			t.Errorf("%s: got %q, want something containing %q", c.name, got, c.reason)
		}
	}
}

func TestPaymentIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := newPaymentID()
		if seen[id] || !strings.HasPrefix(id, "pay_") {
			t.Fatalf("bad or duplicate id %s", id)
		}
		seen[id] = true
	}
}
