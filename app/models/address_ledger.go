package models

// AddressLedgerEntry is what the recorded transactions of one address explain of one
// asset (TokenContract empty for the chain's native asset): the confirmed amounts that
// came in (deposits, sweeps into it, gas seeds) and went out (withdrawals, sweeps and
// gas seeds it sent), in base units as numeric text, and how many of those rows are
// still pending or confirming.
type AddressLedgerEntry struct {
	Address       string
	Asset         string
	TokenContract string
	Credited      string
	Debited       string
	InFlight      int
}

// AddressFeeEntry is what the outbound rows of one address (withdrawals, sweeps and
// gas seeds it sent, of any asset) cost it in native fees: the fees recorded on the
// confirmed rows and on the rows that failed on chain, in base units as numeric text;
// how many confirmed rows have no fee recorded; and how many are still pending or
// confirming.
type AddressFeeEntry struct {
	Address     string
	Fees        string
	MissingFees int
	InFlight    int
}
