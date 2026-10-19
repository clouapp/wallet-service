package chains

// RPCEndpointAddSwagger documents the body of POST .../rpc-endpoints.
type RPCEndpointAddSwagger struct {
	URL      string `json:"url" example:"https://sepolia.drpc.org"`
	Position int    `json:"position" example:"2"`
}

// RPCEndpointListSwagger documents the body of PUT .../rpc-endpoints.
type RPCEndpointListSwagger struct {
	URLs []string `json:"urls" example:"https://sepolia.drpc.org,https://sepolia.gateway.tenderly.co,https://ethereum-sepolia-rpc.publicnode.com"`
}

// RPCEndpointOrderSwagger documents the body of POST .../rpc-endpoints/order.
type RPCEndpointOrderSwagger struct {
	Order []int `json:"order" example:"2,1,3"`
}

// RPCEndpointEnabledSwagger documents the body of PATCH .../rpc-endpoints/{position}.
type RPCEndpointEnabledSwagger struct {
	Enabled bool `json:"enabled" example:"false"`
}
