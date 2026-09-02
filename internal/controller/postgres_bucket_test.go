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
		postgres  string
		uid       types.UID
		want      string
	}{
		{
			name:      "identifies its owner",
			prefix:    "nais-wal-dev-nais-dev",
			namespace: "basseng",
			postgres:  "johnny",
			uid:       "73ecc148-047b-4db8-abe4-cf6a4c0c2b12",
			want:      "nais-wal-dev-nais-dev-basseng-johnny-73ecc148047b",
		},
		{
			name:      "normalizes a trailing prefix separator",
			prefix:    "nais-wal-dev-nais-dev-",
			namespace: "basseng",
			postgres:  "mydb",
			uid:       "f43d742c-26d9-48d0-a629-19553939285d",
			want:      "nais-wal-dev-nais-dev-basseng-mydb-f43d742c26d9",
		},
		{
			name:      "shortens long owner names",
			prefix:    "nais-wal-tenant-environment",
			namespace: strings.Repeat("namespace", 7),
			postgres:  strings.Repeat("postgres", 8),
			uid:       "feedab1e-beef-cafe-babe-700d1e100d1e",
			want:      "nais-wal-tenant-environment-namespacen-postgrespos-feedab1ebeef",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &PostgresReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: tt.prefix}}}
			postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{
				Name:      tt.postgres,
				Namespace: tt.namespace,
				UID:       tt.uid,
			}}

			got := r.bucketName(postgres)
			if got != tt.want {
				t.Errorf("bucketName() = %q, want %q", got, tt.want)
			}
			if len(got) > gcsBucketNameMaxLength {
				t.Errorf("bucketName() length = %d, want at most %d", len(got), gcsBucketNameMaxLength)
			}
		})
	}
}

func TestBucketNameChangesWhenPostgresIsRecreated(t *testing.T) {
	r := &PostgresReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "nais-wal-dev-nais-dev"}}}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{
		Name:      "mydb",
		Namespace: "basseng",
		UID:       "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
	}}

	first := r.bucketName(postgres)
	postgres.UID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	second := r.bucketName(postgres)

	if first == second {
		t.Errorf("recreated Postgres reused bucket name %q", first)
	}
}

func TestBucketNameDisabled(t *testing.T) {
	r := &PostgresReconciler{Config: &config.Config{}}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "mydb", Namespace: "basseng"}}

	if got := r.bucketName(postgres); got != "" {
		t.Errorf("bucketName() = %q, want empty name when WAL archiving is disabled", got)
	}
}
