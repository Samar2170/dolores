package llm

import (
	"context"
	"errors"
	"testing"
)

func TestBudgetUnlimited(t *testing.T) {
	b := NewBudget(0, 0)
	for i := 0; i < 100; i++ {
		if err := b.reserve(); err != nil {
			t.Fatalf("reserve %d: unexpected error: %v", i, err)
		}
		b.record(1000)
	}
	reqs, toks := b.Snapshot()
	if reqs != 100 || toks != 100000 {
		t.Fatalf("snapshot = (%d, %d), want (100, 100000)", reqs, toks)
	}
}

func TestBudgetRequestLimit(t *testing.T) {
	b := NewBudget(2, 0)
	if err := b.reserve(); err != nil {
		t.Fatalf("reserve 1: %v", err)
	}
	if err := b.reserve(); err != nil {
		t.Fatalf("reserve 2: %v", err)
	}
	err := b.reserve()
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("reserve 3: err = %v, want ErrBudgetExhausted", err)
	}
	if !b.exhausted() {
		t.Fatal("exhausted() = false, want true")
	}
}

func TestBudgetTokenLimit(t *testing.T) {
	b := NewBudget(10, 1000)
	if err := b.reserve(); err != nil {
		t.Fatalf("reserve 1: %v", err)
	}
	b.record(600)
	if err := b.reserve(); err != nil {
		t.Fatalf("reserve 2: %v", err)
	}
	b.record(400)
	err := b.reserve()
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("reserve 3: err = %v, want ErrBudgetExhausted", err)
	}
}

func TestBudgetRecordIgnoresNonPositive(t *testing.T) {
	b := NewBudget(0, 100)
	b.record(0)
	b.record(-5)
	if _, toks := b.Snapshot(); toks != 0 {
		t.Fatalf("tokens = %d, want 0", toks)
	}
}

func TestBudgetNilReceiver(t *testing.T) {
	var b *Budget
	if err := b.reserve(); err != nil {
		t.Fatalf("reserve on nil: %v", err)
	}
	b.record(10)
	reqs, toks := b.Snapshot()
	if reqs != 0 || toks != 0 {
		t.Fatalf("snapshot on nil = (%d, %d), want (0, 0)", reqs, toks)
	}
	if b.exhausted() {
		t.Fatal("nil budget exhausted, want false")
	}
}

func TestBudgetContext(t *testing.T) {
	if budgetFrom(context.Background()) != nil {
		t.Fatal("budgetFrom on plain ctx = non-nil, want nil")
	}
	if Exhausted(context.Background()) {
		t.Fatal("Exhausted on plain ctx = true, want false")
	}

	ctx := WithBudget(context.Background(), NewBudget(1, 0))
	b := budgetFrom(ctx)
	if b == nil {
		t.Fatal("budgetFrom = nil, want budget")
	}
	if Exhausted(ctx) {
		t.Fatal("Exhausted before reserve = true, want false")
	}
	if err := b.reserve(); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if !Exhausted(ctx) {
		t.Fatal("Exhausted after reserve = false, want true")
	}
}
