package fixtures

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
)

// runNonce identifies this process. sequence numbers the fixtures it creates.
// The pair keys e-mails, account ids, wallet labels, and client IPs, so a later
// test or a later run does not reuse a value in a store that outlives the
// process. Callers do not reset the schema between tests.
var (
	runNonce = newRunNonce()
	sequence atomic.Uint64
	// fixtureSpace is the UUID v5 namespace for account ids. It is not a row.
	fixtureSpace = uuid.MustParse("a6f0c9e2-4b17-4c8d-9e3a-1d2b3c4d5e6f")
)

func newRunNonce() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("fixture run nonce: %v", err))
	}
	return hex.EncodeToString(buf)
}

func nextToken() string {
	return runNonce + "-" + strconv.FormatUint(sequence.Add(1), 10)
}

// Email is a mailbox unique to this run.
func Email() string {
	return "fixture-" + nextToken() + "@example.com"
}

// AccountID is a UUID v5 of this run's nonce and the next sequence value.
func AccountID() uuid.UUID {
	return uuid.NewSHA1(fixtureSpace, []byte("account:"+nextToken()))
}

// WalletLabel is the chain's wallet label, unique to this run.
func WalletLabel(chainID string) string {
	return chainID + " test wallet " + nextToken()
}

// ClientIP is a documentation IPv6 address unique to this run.
func ClientIP() string {
	nonce, seq, _ := strings.Cut(nextToken(), "-")
	n, _ := strconv.ParseUint(seq, 10, 64)
	seqHex := fmt.Sprintf("%016x", n)
	return fmt.Sprintf("2001:db8:%s:%s:%s:%s:%s:%s",
		nonce[0:4], nonce[4:8],
		seqHex[0:4], seqHex[4:8], seqHex[8:12], seqHex[12:16])
}
