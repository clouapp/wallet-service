package models

import "testing"

func networkIDPointer(id int64) *int64 { return &id }

func TestChainNetworkFollowsTheConfiguredNetworkNotTheChainID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		chain Chain
		want  string
	}{
		{"ethereum mainnet", Chain{ID: ChainETH, AdapterType: AdapterTypeEVM, NetworkID: networkIDPointer(EVMNetworkIDEthereumMainnet)}, NetworkEthereumMainnet},
		{"sepolia", Chain{ID: ChainTETH, AdapterType: AdapterTypeEVM, NetworkID: networkIDPointer(EVMNetworkIDEthereumSepolia), IsTestnet: true}, NetworkEthereumSepolia},
		{"polygon mainnet", Chain{ID: ChainPolygon, AdapterType: AdapterTypeEVM, NetworkID: networkIDPointer(EVMNetworkIDPolygonMainnet)}, NetworkPolygonMainnet},
		{"polygon record configured for amoy", Chain{ID: ChainPolygon, AdapterType: AdapterTypeEVM, NetworkID: networkIDPointer(EVMNetworkIDPolygonAmoy)}, NetworkPolygonAmoy},
		{"tpolygon", Chain{ID: ChainTPolygon, AdapterType: AdapterTypeEVM, NetworkID: networkIDPointer(EVMNetworkIDPolygonAmoy), IsTestnet: true}, NetworkPolygonAmoy},
		{"bitcoin mainnet", Chain{ID: ChainBTC, AdapterType: AdapterTypeBitcoin}, NetworkBitcoinMainnet},
		{"bitcoin testnet", Chain{ID: ChainTBTC, AdapterType: AdapterTypeBitcoin, IsTestnet: true}, NetworkBitcoinTestnet},
		{"solana mainnet", Chain{ID: ChainSOL, AdapterType: AdapterTypeSolana}, NetworkSolanaMainnet},
		{"solana devnet", Chain{ID: ChainTSOL, AdapterType: AdapterTypeSolana, IsTestnet: true}, NetworkSolanaDevnet},
	}
	for _, tc := range cases {
		if got := tc.chain.Network(); got != tc.want {
			t.Errorf("%s: Network() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestResolveNetworkFlagsTestnetsByTheNetworkActuallyUsed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		chain  *Chain
		rpcURL string
		want   ResolvedNetwork
	}{
		{"polygon record configured for amoy despite is_testnet=false", &Chain{ID: ChainPolygon, AdapterType: AdapterTypeEVM, NetworkID: networkIDPointer(EVMNetworkIDPolygonAmoy)}, "", ResolvedNetwork{Name: NetworkPolygonAmoy, Testnet: true}},
		{"polygon mainnet", &Chain{ID: ChainPolygon, AdapterType: AdapterTypeEVM, NetworkID: networkIDPointer(EVMNetworkIDPolygonMainnet)}, "", ResolvedNetwork{Name: NetworkPolygonMainnet}},
		{"sepolia", &Chain{ID: ChainTETH, AdapterType: AdapterTypeEVM, NetworkID: networkIDPointer(EVMNetworkIDEthereumSepolia), IsTestnet: true}, "", ResolvedNetwork{Name: NetworkEthereumSepolia, Testnet: true}},
		{"bitcoin testnet", &Chain{ID: ChainTBTC, AdapterType: AdapterTypeBitcoin, IsTestnet: true}, "", ResolvedNetwork{Name: NetworkBitcoinTestnet, Testnet: true}},
		{"bitcoin mainnet", &Chain{ID: ChainBTC, AdapterType: AdapterTypeBitcoin}, "", ResolvedNetwork{Name: NetworkBitcoinMainnet}},
		{"bitcoin testnet on a testnet3 esplora", &Chain{ID: ChainBTC, AdapterType: AdapterTypeBitcoin, IsTestnet: true}, "https://blockstream.info/testnet/api", ResolvedNetwork{Name: NetworkBitcoinTestnet, Testnet: true}},
		{"bitcoin testnet on mempool testnet4", &Chain{ID: ChainBTC, AdapterType: AdapterTypeBitcoin, IsTestnet: true}, "https://mempool.space/testnet4/api", ResolvedNetwork{Name: NetworkBitcoinTestnet4, Testnet: true}},
		{"tbtc on a testnet4 host", &Chain{ID: ChainTBTC, AdapterType: AdapterTypeBitcoin, IsTestnet: true}, "https://btc-testnet4.example.com/rpc", ResolvedNetwork{Name: NetworkBitcoinTestnet4, Testnet: true}},
		{"bitcoin mainnet record keeps bc1 even on a testnet4 rpc", &Chain{ID: ChainBTC, AdapterType: AdapterTypeBitcoin}, "https://mempool.space/testnet4/api", ResolvedNetwork{Name: NetworkBitcoinMainnet}},
		{"sol record on a devnet rpc despite is_testnet=false", &Chain{ID: ChainSOL, AdapterType: AdapterTypeSolana}, "https://api.devnet.solana.com", ResolvedNetwork{Name: NetworkSolanaDevnet, Testnet: true}},
		{"sol record on a provider testnet rpc", &Chain{ID: ChainSOL, AdapterType: AdapterTypeSolana}, "https://example.solana-testnet.quiknode.pro/path", ResolvedNetwork{Name: NetworkSolanaTestnet, Testnet: true}},
		{"tsol record on a mainnet rpc", &Chain{ID: ChainTSOL, AdapterType: AdapterTypeSolana, IsTestnet: true}, "https://api.mainnet-beta.solana.com", ResolvedNetwork{Name: NetworkSolanaMainnet}},
		{"sol record whose rpc host names no cluster", &Chain{ID: ChainSOL, AdapterType: AdapterTypeSolana, IsTestnet: true}, "https://rpc.example.com", ResolvedNetwork{Name: NetworkSolanaDevnet, Testnet: true}},
		{"rpc url is ignored for evm", &Chain{ID: ChainPolygon, AdapterType: AdapterTypeEVM, NetworkID: networkIDPointer(EVMNetworkIDPolygonMainnet)}, "https://api.devnet.solana.com", ResolvedNetwork{Name: NetworkPolygonMainnet}},
		{"unlisted evm network falls back to is_testnet", &Chain{ID: ChainPolygon, AdapterType: AdapterTypeEVM, NetworkID: networkIDPointer(80001), IsTestnet: true}, "", ResolvedNetwork{Testnet: true}},
		{"nil chain", nil, "", ResolvedNetwork{}},
	}
	for _, tc := range cases {
		if got := tc.chain.ResolveNetwork(tc.rpcURL); got != tc.want {
			t.Errorf("%s: ResolveNetwork() = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestSolanaNetworkOfRPCURLIgnoresUnparseableInput(t *testing.T) {
	t.Parallel()

	for _, rpcURL := range []string{"", "   ", "not a url", "://devnet", "devnet"} {
		if got := SolanaNetworkOfRPCURL(rpcURL); got != "" {
			t.Errorf("SolanaNetworkOfRPCURL(%q) = %q, want unknown", rpcURL, got)
		}
	}
}

func TestIsBitcoinTestnet4RPCURLMatchesWholePathSegmentsOrHostLabels(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"https://mempool.space/testnet4/api":     true,
		"https://mempool.space/testnet4/api/":    true,
		"  https://MEMPOOL.space/TestNet4/api  ": true,
		"https://testnet4.example.com":           true,
		"https://btc-testnet4.example.com:8332":  true,
		"https://mempool.space/testnet/api":      false,
		"https://blockstream.info/testnet/api":   false,
		"https://mempool.space/api":              false,
		"https://mempool.space/testnet40/api":    false,
		"https://example.com/api?net=testnet4":   false,
		"https://testnet4example.com/api":        false,
		"env:BTC_RPC_URL":                        false,
		"":                                       false,
		"not a url":                              false,
	}
	for rpcURL, want := range cases {
		if got := IsBitcoinTestnet4RPCURL(rpcURL); got != want {
			t.Errorf("IsBitcoinTestnet4RPCURL(%q) = %t, want %t", rpcURL, got, want)
		}
	}
}

func TestBitcoinTestnet4IsATestnet(t *testing.T) {
	t.Parallel()

	if !IsTestnetNetwork(NetworkBitcoinTestnet4) || !IsTestnetNetwork(NetworkBitcoinTestnet) || IsTestnetNetwork(NetworkBitcoinMainnet) {
		t.Fatal("testnet3 and testnet4 are testnets, mainnet is not")
	}
}

func TestChainNetworkIsUnknownWithoutEnoughMetadata(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		chain *Chain
	}{
		{"nil chain", nil},
		{"evm without network id", &Chain{ID: ChainPolygon, AdapterType: AdapterTypeEVM}},
		{"evm with an unlisted network id", &Chain{ID: ChainPolygon, AdapterType: AdapterTypeEVM, NetworkID: networkIDPointer(80001)}},
		{"unknown adapter", &Chain{ID: "doge", AdapterType: "utxo"}},
	}
	for _, tc := range cases {
		if got := tc.chain.Network(); got != "" {
			t.Errorf("%s: Network() = %q, want unknown", tc.name, got)
		}
	}
}
