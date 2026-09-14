package storage

import (
	"testing"

	"github.com/cloudnative-pg/barman-cloud/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCreateObjectStoreUsesLz4Compression(t *testing.T) {
	objectStore := CreateObjectStore("wal-bucket", metav1.ObjectMeta{Namespace: "team"})

	if got := objectStore.Spec.Configuration.Data.Compression; got != api.CompressionTypeLz4 {
		t.Errorf("base backup compression = %q, want %q", got, api.CompressionTypeLz4)
	}
	if got := objectStore.Spec.Configuration.Wal.Compression; got != api.CompressionTypeLz4 {
		t.Errorf("WAL compression = %q, want %q", got, api.CompressionTypeLz4)
	}
}
