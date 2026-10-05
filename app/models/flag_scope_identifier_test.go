package models

import (
	"testing"

	"github.com/google/uuid"
)

func TestFlagScopeIdentifierConcatenatesPrefixAndID(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("6f1c0c3e-1b4a-4e3a-9c2d-7a8b9c0d1e2f")
	raw := id.String()
	account := Account{ID: id}
	user := User{ID: id}
	chain := Chain{ID: raw}

	gotAccount := account.FlagScopeIdentifier()
	gotUser := user.FlagScopeIdentifier()
	gotChain := chain.FlagScopeIdentifier()

	if gotAccount != "account:"+raw {
		t.Fatalf("account FlagScopeIdentifier() = %q, want %q", gotAccount, "account:"+raw)
	}
	if gotUser != "user:"+raw {
		t.Fatalf("user FlagScopeIdentifier() = %q, want %q", gotUser, "user:"+raw)
	}
	if gotChain != "chain:"+raw {
		t.Fatalf("chain FlagScopeIdentifier() = %q, want %q", gotChain, "chain:"+raw)
	}
	if gotAccount == gotUser || gotAccount == gotChain || gotUser == gotChain {
		t.Fatalf("scope prefixes must differ: account %q user %q chain %q", gotAccount, gotUser, gotChain)
	}
}

func TestFlagScopeIdentifierKeepsTheZeroID(t *testing.T) {
	t.Parallel()

	var account Account
	var user User
	var chain Chain

	if got, want := account.FlagScopeIdentifier(), "account:"+account.ID.String(); got != want {
		t.Fatalf("zero account FlagScopeIdentifier() = %q, want %q", got, want)
	}
	if got, want := user.FlagScopeIdentifier(), "user:"+user.ID.String(); got != want {
		t.Fatalf("zero user FlagScopeIdentifier() = %q, want %q", got, want)
	}
	if got, want := chain.FlagScopeIdentifier(), "chain:"+chain.ID; got != want {
		t.Fatalf("zero chain FlagScopeIdentifier() = %q, want %q", got, want)
	}
	if got := chain.FlagScopeIdentifier(); got != "chain:" {
		t.Fatalf("empty chain id FlagScopeIdentifier() = %q, want %q", got, "chain:")
	}
}
