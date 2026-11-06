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

// PostgresPhysicalName returns the stable identity used to name a Postgres's
// physical resources. It preserves the historical main-branch identity so
// adopting an existing database does not change clusters, roles or archives.
func PostgresPhysicalName(postgresName string) string {
	return PostgresBranchObjectName(postgresName, DefaultBranchName)
}

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
