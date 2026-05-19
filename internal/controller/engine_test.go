package controller

import (
	"strings"
	"testing"

	"github.com/nais/pgrator/pkg/api"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetEngine(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantEngine  string
		wantErr     bool
	}{
		{
			name:        "no annotations defaults to zalando",
			annotations: nil,
			wantEngine:  api.EngineZalando,
		},
		{
			name:        "empty annotation defaults to zalando",
			annotations: map[string]string{},
			wantEngine:  api.EngineZalando,
		},
		{
			name:        "explicit zalando",
			annotations: map[string]string{api.EngineAnnotation: "zalando"},
			wantEngine:  api.EngineZalando,
		},
		{
			name:        "explicit cnpg",
			annotations: map[string]string{api.EngineAnnotation: "cnpg"},
			wantEngine:  api.EngineCNPG,
		},
		{
			name:        "unknown value returns error",
			annotations: map[string]string{api.EngineAnnotation: "cockroachdb"},
			wantErr:     true,
		},
		{
			name:        "empty string value defaults to zalando",
			annotations: map[string]string{api.EngineAnnotation: ""},
			wantEngine:  api.EngineZalando,
		},
		{
			name:        "active-engine takes precedence over engine annotation",
			annotations: map[string]string{api.ActiveEngineAnnotation: api.EngineCNPG, api.EngineAnnotation: api.EngineZalando},
			wantEngine:  api.EngineCNPG,
		},
		{
			name:        "active-engine used even if engine annotation is removed",
			annotations: map[string]string{api.ActiveEngineAnnotation: api.EngineCNPG},
			wantEngine:  api.EngineCNPG,
		},
		{
			name:        "invalid active-engine returns error",
			annotations: map[string]string{api.ActiveEngineAnnotation: "invalid"},
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &data_nais_io_v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}
			got, err := getEngine(obj)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tt.wantEngine {
				t.Errorf("getEngine() = %q, want %q", got, tt.wantEngine)
			}
		})
	}
}

func TestValidateEngineImmutability(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		engine      string
		wantErr     bool
	}{
		{
			name:        "no active-engine annotation allows any engine (first reconcile)",
			annotations: nil,
			engine:      api.EngineCNPG,
			wantErr:     false,
		},
		{
			name:        "matching engine is allowed",
			annotations: map[string]string{api.ActiveEngineAnnotation: api.EngineZalando},
			engine:      api.EngineZalando,
			wantErr:     false,
		},
		{
			name:        "cnpg to cnpg is allowed",
			annotations: map[string]string{api.ActiveEngineAnnotation: api.EngineCNPG},
			engine:      api.EngineCNPG,
			wantErr:     false,
		},
		{
			name:        "zalando to cnpg is rejected",
			annotations: map[string]string{api.ActiveEngineAnnotation: api.EngineZalando},
			engine:      api.EngineCNPG,
			wantErr:     true,
		},
		{
			name:        "cnpg to zalando is rejected",
			annotations: map[string]string{api.ActiveEngineAnnotation: api.EngineCNPG},
			engine:      api.EngineZalando,
			wantErr:     true,
		},
		{
			name:        "empty active-engine allows first choice",
			annotations: map[string]string{api.ActiveEngineAnnotation: ""},
			engine:      api.EngineCNPG,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &data_nais_io_v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}
			err := validateEngineImmutability(obj, tt.engine)
			if tt.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestEngineSelectionAndValidation tests the full engine resolution + validation
// pipeline as experienced by real users. This is the backward-compat safety net.
func TestEngineSelectionAndValidation(t *testing.T) {
	tests := []struct {
		name         string
		annotations  map[string]string
		majorVersion string
		wantErr      bool
		errContains  string
	}{
		// Backward compatibility: existing users without annotations
		{
			name:         "no annotations with v16 defaults to zalando and succeeds",
			annotations:  nil,
			majorVersion: "16",
		},
		{
			name:         "no annotations with v17 defaults to zalando and succeeds",
			annotations:  nil,
			majorVersion: "17",
		},
		{
			name:         "empty annotations with v16 defaults to zalando and succeeds",
			annotations:  map[string]string{},
			majorVersion: "16",
		},
		{
			name:         "empty annotations with v17 defaults to zalando and succeeds",
			annotations:  map[string]string{},
			majorVersion: "17",
		},
		// Existing user tries v18 without engine annotation (should fail)
		{
			name:         "no annotations with v18 defaults to zalando and is rejected",
			annotations:  nil,
			majorVersion: "18",
			wantErr:      true,
			errContains:  "zalando engine only supports majorVersion 16 or 17",
		},
		// Existing zalando user with active-engine set (post first reconcile)
		{
			name:         "active-engine zalando with v16 succeeds",
			annotations:  map[string]string{api.ActiveEngineAnnotation: api.EngineZalando},
			majorVersion: "16",
		},
		{
			name:         "active-engine zalando with v17 succeeds",
			annotations:  map[string]string{api.ActiveEngineAnnotation: api.EngineZalando},
			majorVersion: "17",
		},
		{
			name:         "active-engine zalando with v18 is rejected",
			annotations:  map[string]string{api.ActiveEngineAnnotation: api.EngineZalando},
			majorVersion: "18",
			wantErr:      true,
			errContains:  "zalando engine only supports majorVersion 16 or 17",
		},
		// New CNPG user
		{
			name:         "engine cnpg with v18 succeeds",
			annotations:  map[string]string{api.EngineAnnotation: api.EngineCNPG},
			majorVersion: "18",
		},
		{
			name:         "engine cnpg with v17 is rejected",
			annotations:  map[string]string{api.EngineAnnotation: api.EngineCNPG},
			majorVersion: "17",
			wantErr:      true,
			errContains:  "cnpg engine requires majorVersion >= 18",
		},
		// Active-engine takes precedence: user annotation overridden
		{
			name:         "active-engine zalando overrides engine cnpg annotation",
			annotations:  map[string]string{api.ActiveEngineAnnotation: api.EngineZalando, api.EngineAnnotation: api.EngineCNPG},
			majorVersion: "17",
		},
		{
			name:         "active-engine cnpg with v18 succeeds even without engine annotation",
			annotations:  map[string]string{api.ActiveEngineAnnotation: api.EngineCNPG},
			majorVersion: "18",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &data_nais_io_v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
				Spec: data_nais_io_v1.PostgresSpec{
					Cluster: data_nais_io_v1.PostgresCluster{
						MajorVersion: tt.majorVersion,
					},
				},
			}

			// Simulate the Prepare() validation pipeline
			engine, err := getEngine(obj)
			if err != nil {
				if !tt.wantErr {
					t.Fatalf("unexpected getEngine error: %v", err)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err := validateEngineImmutability(obj, engine); err != nil {
				if !tt.wantErr {
					t.Fatalf("unexpected immutability error: %v", err)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err := validateVersionForEngine(obj.Spec.Cluster.MajorVersion, engine); err != nil {
				if !tt.wantErr {
					t.Fatalf("unexpected version error: %v", err)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if tt.wantErr {
				t.Error("expected error but validation pipeline succeeded")
			}
		})
	}
}

func TestValidateVersionForEngine(t *testing.T) {
	tests := []struct {
		name         string
		majorVersion string
		engine       string
		wantErr      bool
		errContains  string
	}{
		{
			name:         "cnpg with version 18 is valid",
			majorVersion: "18",
			engine:       api.EngineCNPG,
		},
		{
			name:         "cnpg with version 19 is valid",
			majorVersion: "19",
			engine:       api.EngineCNPG,
		},
		{
			name:         "cnpg with version 17 is rejected",
			majorVersion: "17",
			engine:       api.EngineCNPG,
			wantErr:      true,
			errContains:  "cnpg engine requires majorVersion >= 18",
		},
		{
			name:         "cnpg with version 16 is rejected",
			majorVersion: "16",
			engine:       api.EngineCNPG,
			wantErr:      true,
			errContains:  "cnpg engine requires majorVersion >= 18",
		},
		{
			name:         "zalando with version 16 is valid",
			majorVersion: "16",
			engine:       api.EngineZalando,
		},
		{
			name:         "zalando with version 17 is valid",
			majorVersion: "17",
			engine:       api.EngineZalando,
		},
		{
			name:         "zalando with version 18 is rejected",
			majorVersion: "18",
			engine:       api.EngineZalando,
			wantErr:      true,
			errContains:  "zalando engine only supports majorVersion 16 or 17",
		},
		{
			name:         "cnpg with invalid version returns error",
			majorVersion: "abc",
			engine:       api.EngineCNPG,
			wantErr:      true,
			errContains:  "invalid major version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVersionForEngine(tt.majorVersion, tt.engine)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
