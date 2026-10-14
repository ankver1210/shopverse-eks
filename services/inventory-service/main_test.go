package main

import (
	"strings"
	"testing"
)

func TestInsufficientStockMessage(t *testing.T) {
	err := &insufficientStock{ProductID: 9, Requested: 3, Available: 1}
	if !strings.Contains(err.Error(), "product 9") || !strings.Contains(err.Error(), "available 1") {
		t.Fatalf("unexpected message: %s", err)
	}
}

func TestParseIDs(t *testing.T) {
	if _, err := parseIDs("1,x"); err == nil {
		t.Fatal("expected error")
	}
	ids, err := parseIDs("3,1")
	if err != nil || intArray(ids) != "{3,1}" {
		t.Fatalf("got %v %v", ids, err)
	}
}

func TestSeedHasScarceItems(t *testing.T) {
	scarce := 0
	for _, qty := range seedStock {
		if qty <= 10 {
			scarce++
		}
	}
	if scarce == 0 {
		t.Fatal("at least one scarce product is needed to demo the out-of-stock path")
	}
}
