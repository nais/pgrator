package v1_test

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestPostgresBranchAPIContract(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("register nais.io/v1: %v", err)
	}

	for _, kind := range []string{"PostgresBranch", "PostgresBranchList"} {
		if _, err := scheme.New(v1.GroupVersion.WithKind(kind)); err != nil {
			t.Errorf("scheme does not register %s: %v", kind, err)
		}
	}

	cases := []struct {
		name    string
		object  any
		wantKey string
		oldKey  string
	}{
		{"Postgres selection", &v1.Postgres{Spec: v1.PostgresSpec{ActiveBranch: "restore"}, Status: &v1.PostgresStatus{ActiveBranch: "restore"}}, `"activeBranch"`, `"activeInstance"`},
		{"recovery source", &v1.PostgresBranch{Spec: v1.PostgresBranchSpec{Bootstrap: &v1.PostgresBranchBootstrap{Recovery: &v1.PostgresBranchRecovery{SourceBranch: "origin"}}}}, `"sourceBranch"`, `"sourceInstance"`},
		{"personal access", &v1.PostgresAccess{Spec: v1.PostgresAccessSpec{PostgresBranch: "restore"}}, `"postgresBranch"`, `"postgresInstance"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.object)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tc.wantKey) || strings.Contains(string(data), tc.oldKey) {
				t.Errorf("wire format = %s, want %s without %s", data, tc.wantKey, tc.oldKey)
			}
		})
	}
}

func TestLatestRecoveryOmitsTargetTime(t *testing.T) {
	recovery := &v1.PostgresBranchRecovery{SourceBranch: "main"}
	data, err := json.Marshal(recovery)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"sourceBranch":"main"}` {
		t.Fatalf("latest recovery JSON = %s", data)
	}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(recovery)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := obj["targetTime"]; found {
		t.Fatalf("latest recovery must omit targetTime, got %#v", obj)
	}
}
