package resourcecreator

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
)

var minimumDisksizePerStorageClass map[string]resource.Quantity

func init() {
	minimumDisksizePerStorageClass = map[string]resource.Quantity{
		"hyperdisk-balanced": resource.MustParse("4Gi"),
		"hyperdisk-premium":  resource.MustParse("4Gi"),
		"standard-rwo":       resource.MustParse("2Gi"),
		"premium-rwo":        resource.MustParse("2Gi"),
		"":                   resource.MustParse("2Gi"), // Use 2Gi when unset
	}
}

// EnforceMinimumDisk returns the effective disk size, enforcing a per-storage-class minimum.
// Returns an error when the storage class is not recognised.
func EnforceMinimumDisk(diskSize resource.Quantity, storageClass string) (*resource.Quantity, error) {
	if minimum, ok := minimumDisksizePerStorageClass[storageClass]; ok {
		if diskSize.Cmp(minimum) < 0 {
			return &minimum, nil
		}
		return &diskSize, nil
	}
	return nil, fmt.Errorf("no minimum disksize defined for storage class %q (this is a platform error)", storageClass)
}
