package withdrawals

import (
	"context"
	"testing"
)

func TestStoreRequest_DestinationTag_IsAnOptionalUint32(t *testing.T) {
	rules := (&StoreRequest{}).Rules(scopeContext{ctx: context.Background()})
	if got := rules["destination_tag"]; got != "uint32" {
		t.Fatalf("destination_tag rule = %q", got)
	}
}
