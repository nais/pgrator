package namegen

import (
	"fmt"
	"hash/crc32"
)

const (
	SuffixLength = 9 // 8 bytes of hexadecimal hash/random characters and 1 byte of separator
)

// Append the string's CRC32 hash to the string and truncate it to a maximum length.
// Can be used to avoid collisions in the Kubernetes namespace. CRC is deterministic.
//
// e.g. ShortName("foobarbaz", 16) --> "foobarb-12345678", which is longer than the original name
func ShortName(basename string, maxlen int) (string, error) {
	maxlen -= SuffixLength
	hasher := crc32.NewIEEE()
	_, err := hasher.Write([]byte(basename))
	if err != nil {
		return "", err
	}
	hashStr := fmt.Sprintf("%08x", hasher.Sum32())

	return formatName(basename, hashStr, maxlen), nil
}

func MustShortenName(basename string, maxlen int) string {
	res, err := ShortName(basename, maxlen)
	if err != nil {
		panic(fmt.Errorf("failed to shorten name: %w", err))
	}
	return res
}

func formatName(basename, suffix string, maxlen int) string {
	if len(basename) > maxlen {
		basename = basename[:maxlen]
	}
	return fmt.Sprintf("%s-%s", basename, suffix)
}
