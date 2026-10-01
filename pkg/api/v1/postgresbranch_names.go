package v1

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

const (
	branchObjectPrefixMaxLen = 30
	branchObjectHashLen      = 8
)

// PostgresBranchObjectName returns the deterministic metadata.name of the
// PostgresBranch object for a local branch name within a Postgres.
//
// The name is "<postgres>-<branch>" (truncated) plus a hash of the NUL-separated
// pair, distinguishing ambiguous concatenations such as ("a-b","c") and ("a","b-c"). The result is at most 39
// characters so names derived from it (pg-<name>-pooler, ...) stay within limits.
// Consumers must still verify spec.postgres and spec.branchName on lookup.
func PostgresBranchObjectName(postgres, branch string) string {
	prefix := strings.ReplaceAll(postgres+"-"+branch, ".", "-")
	if len(prefix) > branchObjectPrefixMaxLen {
		prefix = strings.TrimRight(prefix[:branchObjectPrefixMaxLen], "-")
	}
	sum := sha256.Sum256([]byte(postgres + "\x00" + branch))
	return fmt.Sprintf("%s-%x", prefix, sum[:branchObjectHashLen/2])
}

// DefaultBranchName is the local name of the branch created with a Postgres.
const DefaultBranchName = "main"
