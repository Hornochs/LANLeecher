package snmp

import (
	"errors"
	"testing"

	"github.com/gosnmp/gosnmp"
)

func fakeWalk(n int) func(string, gosnmp.WalkFunc) error {
	return func(_ string, fn gosnmp.WalkFunc) error {
		for i := 0; i < n; i++ {
			if err := fn(gosnmp.SnmpPDU{Name: ".1.3"}); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestWalkLimited(t *testing.T) {
	old := MaxWalkRows
	MaxWalkRows = 5
	defer func() { MaxWalkRows = old }()

	pdus, err := walkLimited(fakeWalk(5), ".1.3")
	if err != nil || len(pdus) != 5 {
		t.Fatalf("at limit: %d rows, err %v", len(pdus), err)
	}
	if _, err := walkLimited(fakeWalk(6), ".1.3"); !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("over limit: err %v", err)
	}
}
