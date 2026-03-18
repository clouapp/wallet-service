package security

import (
	"regexp"
	"strings"
)

// XSS patterns to detect potentially dangerous HTML/JavaScript
var dangerousHTMLPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)<script[^>]*>.*?</script>`),
	regexp.MustCompile(`(?i)<iframe[^>]*>.*?</iframe>`),
	regexp.MustCompile(`(?i)javascript:`),
	regexp.MustCompile(`(?i)on\w+\s*=`), // onclick, onerror, etc.
	regexp.MustCompile(`(?i)<embed[^>]*>`),
	regexp.MustCompile(`(?i)<object[^>]*>`),
	regexp.MustCompile(`(?i)data:text/html`),
	regexp.MustCompile(`(?i)vbscript:`),
	regexp.MustCompile(`(?i)<meta[^>]*>`),
	regexp.MustCompile(`(?i)<link[^>]*>`),
	regexp.MustCompile(`(?i)<base[^>]*>`),
	regexp.MustCompile(`(?i)<svg[^>]*>.*?</svg>`),
	regexp.MustCompile(`(?i)expression\s*\(`), // CSS expressions
}

// SQL injection patterns
var sqlInjectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(select|union|insert|update|delete|drop|alter|truncate|exec|execute)\b`),
	regexp.MustCompile(`(?i)(--|\bOR\b|\bAND\b)\s+['"]?\d+['"]?\s*=?\s*['"]?\d*['"]?`),
	regexp.MustCompile(`(?i)['"]\s*=\s*['"]`),
	regexp.MustCompile(`(?i);.*?(drop|delete|update|insert)`),
	regexp.MustCompile(`(?i)'\s*(or|and)\s*'?\d+'\s*=\s*'?\d+`),
}

// IsDangerousHTML checks if the input string contains potentially dangerous HTML/JavaScript
func IsDangerousHTML(input string) bool {
	if input == "" {
		return false
	}

	// Decode common HTML entities first
	decoded := strings.ReplaceAll(input, "&lt;", "<")
	decoded = strings.ReplaceAll(decoded, "&gt;", ">")
	decoded = strings.ReplaceAll(decoded, "&#x3C;", "<")
	decoded = strings.ReplaceAll(decoded, "&#x3E;", ">")
	decoded = strings.ReplaceAll(decoded, "&#60;", "<")
	decoded = strings.ReplaceAll(decoded, "&#62;", ">")

	for _, pattern := range dangerousHTMLPatterns {
		if pattern.MatchString(decoded) {
			return true
		}
	}

	return false
}

// ContainsDangerousHTML recursively checks if data contains dangerous HTML
func ContainsDangerousHTML(data interface{}) bool {
	switch val := data.(type) {
	case string:
		return IsDangerousHTML(val)
	case map[string]interface{}:
		for _, v := range val {
			if ContainsDangerousHTML(v) {
				return true
			}
		}
	case []interface{}:
		for _, v := range val {
			if ContainsDangerousHTML(v) {
				return true
			}
		}
	}
	return false
}

// IsSQLInjection checks if the input string contains SQL injection patterns
func IsSQLInjection(input string) bool {
	if input == "" {
		return false
	}

	for _, pattern := range sqlInjectionPatterns {
		if pattern.MatchString(input) {
			return true
		}
	}

	return false
}

// ContainsSQLInjection recursively checks if data contains SQL injection patterns
func ContainsSQLInjection(data interface{}) bool {
	switch val := data.(type) {
	case string:
		return IsSQLInjection(val)
	case map[string]interface{}:
		for _, v := range val {
			if ContainsSQLInjection(v) {
				return true
			}
		}
	case []interface{}:
		for _, v := range val {
			if ContainsSQLInjection(v) {
				return true
			}
		}
	}
	return false
}

// SanitizeHTML removes dangerous HTML tags and attributes from input
func SanitizeHTML(input string) string {
	// Simple sanitization: remove script tags and event handlers
	sanitized := input

	// Remove script tags
	scriptPattern := regexp.MustCompile(`(?i)<script[^>]*>.*?</script>`)
	sanitized = scriptPattern.ReplaceAllString(sanitized, "")

	// Remove event handlers
	eventPattern := regexp.MustCompile(`(?i)on\w+\s*=\s*["'][^"']*["']`)
	sanitized = eventPattern.ReplaceAllString(sanitized, "")

	// Remove javascript: protocol
	jsPattern := regexp.MustCompile(`(?i)javascript:`)
	sanitized = jsPattern.ReplaceAllString(sanitized, "")

	return sanitized
}
