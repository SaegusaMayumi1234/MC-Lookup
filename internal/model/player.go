package model

import (
	"regexp"
	"strings"
)

// Player represents a Minecraft player with UUID and username
type Player struct {
	UUID     string `json:"uuid"`
	Username string `json:"username"`
}

// uuidRegex matches UUIDs with or without dashes
var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$`)

// usernameRegex matches valid Minecraft usernames (3-16 alphanumeric + underscore)
var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,16}$`)

// IsUUID checks if the identifier looks like a UUID
func IsUUID(identifier string) bool {
	return uuidRegex.MatchString(identifier)
}

// IsValidUsername checks if the identifier is a valid Minecraft username
func IsValidUsername(identifier string) bool {
	return usernameRegex.MatchString(identifier)
}

// IsValidIdentifier checks if the identifier is either a valid UUID or username
func IsValidIdentifier(identifier string) bool {
	return IsUUID(identifier) || IsValidUsername(identifier)
}

// NormalizeUUID converts a UUID to the standard dashed format
// Input: 069a79f444e94726a5befca90e38aaf5 or 069a79f4-44e9-4726-a5be-fca90e38aaf5
// Output: 069a79f4-44e9-4726-a5be-fca90e38aaf5
func NormalizeUUID(uuid string) string {
	// Remove existing dashes
	uuid = strings.ReplaceAll(uuid, "-", "")
	uuid = strings.ToLower(uuid)

	if len(uuid) != 32 {
		return uuid
	}

	// Insert dashes at correct positions
	return uuid[0:8] + "-" + uuid[8:12] + "-" + uuid[12:16] + "-" + uuid[16:20] + "-" + uuid[20:32]
}

// NormalizeIdentifier normalizes the identifier for cache key consistency
// UUIDs are converted to lowercase dashed format
// Usernames are converted to lowercase
func NormalizeIdentifier(identifier string) string {
	if IsUUID(identifier) {
		return NormalizeUUID(identifier)
	}
	return strings.ToLower(identifier)
}
