package controller

import (
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
