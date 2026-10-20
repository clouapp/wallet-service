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
// gas seeds it sent, of any asset) cost it in native base units beside the amounts
// they moved, as numeric text: the fees recorded on the confirmed rows and on the
// rows that failed on chain, plus the account rent they paid (raw_payload
// account_rent, Solana). MissingFees counts the confirmed rows, and those mined and
// failed on chain, with no fee recorded; MissingRent the confirmed rows with no
// account rent recorded, on a chain that records it; InFlight the rows still pending
// or confirming.
type AddressFeeEntry struct {
	Address     string
	Fees        string
	MissingFees int
	MissingRent int
	InFlight    int
}
