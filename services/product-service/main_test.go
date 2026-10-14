package main

import "testing"

func TestParseIDs(t *testing.T) {
	ids, err := parseIDs("1, 2,3")
	if err != nil || len(ids) != 3 || ids[2] != 3 {
		t.Fatalf("unexpected result %v %v", ids, err)
	}
	for _, bad := range []string{"", "a,b", "0", "-1"} {
		if _, err := parseIDs(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestIntArray(t *testing.T) {
	if got := intArray([]int{4, 5}); got != "{4,5}" {
		t.Fatalf("got %s", got)
	}
}

func TestSeedCatalogIsValid(t *testing.T) {
	seen := map[int]bool{}
	for _, p := range seedProducts {
		if seen[p.ID] || p.PriceCents <= 0 || p.Name == "" || p.Category == "" {
			t.Errorf("invalid seed product %+v", p)
		}
		seen[p.ID] = true
	}
}
