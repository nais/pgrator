package v1_test

import (
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
)

func TestPostgresSpecBackupRetentionDays(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec v1.PostgresSpec
		want int32
	}{
		{
			name: "defaults when neither pitr nor legacy backup is set",
			spec: v1.PostgresSpec{},
			want: v1.DefaultBackupRetentionDays,
		},
		{
			name: "uses spec.pitr.windowDays when set",
			spec: v1.PostgresSpec{
				PITR: &v1.PostgresPITRPolicy{WindowDays: new(int32(14))},
			},
			want: 14,
		},
		{
			name: "falls back to deprecated spec.backup.retentionDays",
			spec: v1.PostgresSpec{
				Backup: &v1.PostgresBackupPolicy{RetentionDays: new(int32(7))},
			},
			want: 7,
		},
		{
			name: "prefers spec.pitr.windowDays over deprecated spec.backup.retentionDays",
			spec: v1.PostgresSpec{
				PITR:   &v1.PostgresPITRPolicy{WindowDays: new(int32(14))},
				Backup: &v1.PostgresBackupPolicy{RetentionDays: new(int32(7))},
			},
			want: 14,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.BackupRetentionDays(); got != tc.want {
				t.Errorf("BackupRetentionDays() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPostgresBackupEffectiveRetentionDays(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec v1.PostgresBackupSpec
		want int32
	}{
		{
			name: "defaults to 30 days when omitted",
			spec: v1.PostgresBackupSpec{Postgres: "orders"},
			want: v1.DefaultPostgresBackupRetentionDays,
		},
		{
			name: "uses explicit retentionDays",
			spec: v1.PostgresBackupSpec{Postgres: "orders", RetentionDays: new(int32(365))},
			want: 365,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.EffectiveRetentionDays(); got != tc.want {
				t.Errorf("EffectiveRetentionDays() = %d, want %d", got, tc.want)
			}
		})
	}
}
