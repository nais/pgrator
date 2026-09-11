package controller

import (
	"strings"
	"testing"

	"github.com/nais/pgrator/internal/config"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestBucketName(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		namespace string
		instance  string
		uid       types.UID
		want      string
	}{
		{
			name:      "identifies its owner",
			prefix:    "nais-wal-dev-nais-dev",
			namespace: "basseng",
			instance:  "johnny",
			uid:       "73ecc148-047b-4db8-abe4-cf6a4c0c2b12",
			want:      "nais-wal-dev-nais-dev-basseng-johnny-73ecc148047b",
		},
		{
			name:      "normalizes a trailing prefix separator",
			prefix:    "nais-wal-dev-nais-dev-",
			namespace: "basseng",
			instance:  "mydb",
			uid:       "f43d742c-26d9-48d0-a629-19553939285d",
			want:      "nais-wal-dev-nais-dev-basseng-mydb-f43d742c26d9",
		},
		{
			name:      "shortens long owner names",
			prefix:    "nais-wal-tenant-environment",
			namespace: strings.Repeat("namespace", 7),
			instance:  strings.Repeat("postgres", 8),
			uid:       "feedab1e-beef-cafe-babe-700d1e100d1e",
			want:      "nais-wal-tenant-environment-namespacen-postgrespos-feedab1ebeef",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &PostgresInstanceReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: tt.prefix}}}
			instance := &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: tt.instance, Namespace: tt.namespace}}

			got := r.bucketName(instance, PostgresInstancePreparedData{PostgresUID: tt.uid})
			if got != tt.want {
				t.Errorf("bucketName() = %q, want %q", got, tt.want)
			}
			if len(got) > instanceBucketNameMaxLength {
				t.Errorf("bucketName() length = %d, want at most %d", len(got), instanceBucketNameMaxLength)
			}
		})
	}
}

func TestBucketNameChangesWhenPostgresIsRecreated(t *testing.T) {
	r := &PostgresInstanceReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "nais-wal-dev-nais-dev"}}}
	instance := &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "mydb", Namespace: "basseng"}}

	first := r.bucketName(instance, PostgresInstancePreparedData{PostgresUID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"})
	second := r.bucketName(instance, PostgresInstancePreparedData{PostgresUID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"})

	if first == second {
		t.Errorf("recreated Postgres reused bucket name %q", first)
	}
}

func TestBucketNameDisabled(t *testing.T) {
	r := &PostgresInstanceReconciler{Config: &config.Config{}}
	instance := &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "mydb", Namespace: "basseng"}}

	if got := r.bucketName(instance, PostgresInstancePreparedData{}); got != "" {
		t.Errorf("bucketName() = %q, want empty name when WAL archiving is disabled", got)
	}
}

func TestGSANameFor(t *testing.T) {
	name := gsaNameFor("dagens-postgres-restored-20260911-1200")
	if len(name) > gcpServiceAccountIDMaxLength {
		t.Errorf("gsaNameFor() length = %d, want at most %d", len(name), gcpServiceAccountIDMaxLength)
	}
	if name != "cnpg-dagens-postgres--b7d68638" {
		t.Errorf("gsaNameFor() = %q, want %q", name, "cnpg-dagens-postgres--b7d68638")
	}
}
