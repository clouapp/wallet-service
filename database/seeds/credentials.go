package seeds

import "fmt"

// PrintCredentials logs dev login hints to stdout after seeding.
func PrintCredentials() {
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  Seed data created — login credentials")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  admin@macro.markets / secret  (account owner)")
	fmt.Println("  alice@macro.markets / secret  (account admin)")
	fmt.Println("  bob@macro.markets   / secret  (auditor)")
	fmt.Println()
	fmt.Println("  Accounts: Acme Corp (prod) · Acme Corp (Test) — paired, default = prod")
	fmt.Println("  Wallets: ETH · BTC · Polygon (prod) · Sepolia · Bitcoin testnet · Amoy (test)")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
}
