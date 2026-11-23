package v1

import (
	"crypto/sha256"
	"fmt"
	"hash/crc32"
	"strings"
)

// DefaultBranchName is the only value accepted by the deprecated branch fields
// (Postgres activeBranch, PostgresBinding branch). It is also part of every
// physical name, see PostgresPhysicalName.
const DefaultBranchName = "main"

const (
	physicalNamePrefixMaxLen = 30
	physicalNameHashLen      = 8

	cnpgClusterNamePrefix = "pg-"
	cnpgClusterNameMaxLen = 50
	nameHashLen           = 9 // separator and eight hexadecimal CRC32 characters
)

// PostgresPhysicalName returns the stable identity used to name a Postgres's
// physical resources: "<postgres>-main" (truncated) plus a hash. The format is
// frozen; changing it would rename existing clusters, roles and archives.
// The result is at most 39 characters so derived names (pg-<name>-pooler, ...)
// stay within limits.
func PostgresPhysicalName(postgresName string) string {
	prefix := strings.ReplaceAll(postgresName+"-"+DefaultBranchName, ".", "-")
	if len(prefix) > physicalNamePrefixMaxLen {
		prefix = strings.TrimRight(prefix[:physicalNamePrefixMaxLen], "-")
	}
	sum := sha256.Sum256([]byte(postgresName + "\x00" + DefaultBranchName))
	return fmt.Sprintf("%s-%x", prefix, sum[:physicalNameHashLen/2])
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
