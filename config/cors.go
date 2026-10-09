package config

import "strings"

// defaultCorsOrigins are the local front ends, used when CORS_ALLOWED_ORIGINS
// is unset.
var defaultCorsOrigins = []string{"http://localhost:3000", "http://localhost:3001"}

// corsOrigins parses CORS_ALLOWED_ORIGINS, a comma-separated list. It is read
// once at boot (http.cors_allowed_origins) and handed to middleware.Cors.
func corsOrigins(raw string) []string {
	var origins []string
	for _, origin := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	if len(origins) == 0 {
		return append([]string(nil), defaultCorsOrigins...)
	}
	return origins
}
