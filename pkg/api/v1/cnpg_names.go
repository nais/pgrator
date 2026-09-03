package v1

import (
	"fmt"
	"hash/crc32"
	"strings"
)

const (
	cnpgClusterNamePrefix = "pg-"
	cnpgClusterNameMaxLen = 50
	nameHashLen           = 9 // separator and eight hexadecimal CRC32 characters
)

// CNPGClusterName returns the CloudNativePG Cluster name for a Postgres name.
func CNPGClusterName(postgresName string) string {
	name := cnpgClusterNamePrefix + postgresName
	normalized := strings.ReplaceAll(name, ".", "-")
	if normalized == name && len(name) <= cnpgClusterNameMaxLen {
		return name
	}

	prefixLen := cnpgClusterNameMaxLen - nameHashLen
	if len(normalized) > prefixLen {
		normalized = normalized[:prefixLen]
	}
	return fmt.Sprintf("%s-%08x", normalized, crc32.ChecksumIEEE([]byte(name)))
}
