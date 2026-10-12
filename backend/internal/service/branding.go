package service

import "strings"

const defaultSiteName = "ai-gateway"

// normalizeSiteName replaces the legacy default while preserving custom branding.
func normalizeSiteName(value string) string {
	name := strings.TrimSpace(value)
	if name == "" || strings.EqualFold(name, "sub2api") {
		return defaultSiteName
	}
	return value
}
