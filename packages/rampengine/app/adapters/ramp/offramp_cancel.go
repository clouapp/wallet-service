package ramp

import "context"

// OfframpCanceler is a provider that can withdraw an off-ramp order nobody has
// funded yet.
//
// The app learns where to deposit only from the created order, and only then
// can it tell whether the wallet pays for that deposit: the first deposit to a
// Bridge customer's address also creates the address's token account, and the
// sender pays its rent. An order the wallet then refuses to fund would sit in
// `awaiting_funds` forever, so the app cancels it.
//
// Optional for the same reason OnrampDeliveryAdapter is: the service
// type-asserts it off the adapter the factory resolves, and a provider without
// it refuses the cancel.
type OfframpCanceler interface {
	Code() ProviderCode
	// CancelOfframp withdraws the order. The provider refuses it once the
	// deposit has arrived; nil means the order is canceled upstream.
	CancelOfframp(ctx context.Context, ref CustomerRef, providerOrderID string) error
}
