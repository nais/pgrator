package controller

import (
	"context"
	"testing"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/pkg/api"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	core_v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPrepareStampsActiveEngineAnnotation(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = core_v1.AddToScheme(scheme)
	_ = data_nais_io_v1.AddToScheme(scheme)

	teamNamespace := &core_v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-team",
			Labels: map[string]string{
				ProjectIDLabel: "test-project",
			},
		},
	}

	tests := []struct {
		name           string
		annotations    map[string]string
		majorVersion   string
		wantEngine     string
		wantAnnotation string
	}{
		{
			name:           "no annotations stamps zalando",
			annotations:    nil,
			majorVersion:   "16",
			wantEngine:     api.EngineZalando,
			wantAnnotation: api.EngineZalando,
		},
		{
			name:           "empty annotations stamps zalando",
			annotations:    map[string]string{},
			majorVersion:   "17",
			wantEngine:     api.EngineZalando,
			wantAnnotation: api.EngineZalando,
		},
		{
			name:           "explicit cnpg engine stamps cnpg",
			annotations:    map[string]string{api.EngineAnnotation: api.EngineCNPG},
			majorVersion:   "18",
			wantEngine:     api.EngineCNPG,
			wantAnnotation: api.EngineCNPG,
		},
		{
			name:           "explicit zalando engine stamps zalando",
			annotations:    map[string]string{api.EngineAnnotation: api.EngineZalando},
			majorVersion:   "16",
			wantEngine:     api.EngineZalando,
			wantAnnotation: api.EngineZalando,
		},
		{
			name:           "existing active-engine preserved",
			annotations:    map[string]string{api.ActiveEngineAnnotation: api.EngineCNPG},
			majorVersion:   "18",
			wantEngine:     api.EngineCNPG,
			wantAnnotation: api.EngineCNPG,
		},
		{
			name:           "pre-cnpg resource without any engine annotation gets zalando stamped",
			annotations:    map[string]string{"some-other-annotation": "value"},
			majorVersion:   "16",
			wantEngine:     api.EngineZalando,
			wantAnnotation: api.EngineZalando,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(teamNamespace.DeepCopy()).
				Build()

			reconciler := &PostgresReconciler{
				Config: &config.Config{},
			}

			obj := &data_nais_io_v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-db",
					Namespace:   "my-team",
					Annotations: tt.annotations,
				},
				Spec: data_nais_io_v1.PostgresSpec{
					Cluster: data_nais_io_v1.PostgresCluster{
						MajorVersion: tt.majorVersion,
						Resources: data_nais_io_v1.PostgresResources{
							DiskSize: resource.MustParse("10Gi"),
							Memory:   resource.MustParse("1Gi"),
						},
					},
				},
			}

			prep, _, err := reconciler.Prepare(context.Background(), fakeClient, obj)
			if err != nil {
				t.Fatalf("Prepare() returned unexpected error: %v", err)
			}

			// Verify PreparedData has correct engine
			if prep.Engine != tt.wantEngine {
				t.Errorf("PreparedData.Engine = %q, want %q", prep.Engine, tt.wantEngine)
			}

			// Verify the active-engine annotation was stamped on the object
			gotAnnotation := obj.Annotations[api.ActiveEngineAnnotation]
			if gotAnnotation != tt.wantAnnotation {
				t.Errorf("obj.Annotations[%q] = %q, want %q", api.ActiveEngineAnnotation, gotAnnotation, tt.wantAnnotation)
			}
		})
	}
}

func TestPrepareAnnotationStampingWithNilAnnotations(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = core_v1.AddToScheme(scheme)
	_ = data_nais_io_v1.AddToScheme(scheme)

	teamNamespace := &core_v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-team",
			Labels: map[string]string{
				ProjectIDLabel: "test-project",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(teamNamespace).
		Build()

	reconciler := &PostgresReconciler{
		Config: &config.Config{},
	}

	// Simulate an old Postgres resource with absolutely no annotations (nil map)
	obj := &data_nais_io_v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "legacy-db",
			Namespace: "my-team",
		},
		Spec: data_nais_io_v1.PostgresSpec{
			Cluster: data_nais_io_v1.PostgresCluster{
				MajorVersion: "16",
				Resources: data_nais_io_v1.PostgresResources{
					DiskSize: resource.MustParse("10Gi"),
					Memory:   resource.MustParse("1Gi"),
				},
			},
		},
	}

	if obj.Annotations != nil {
		t.Fatal("precondition: annotations should be nil")
	}

	_, _, err := reconciler.Prepare(context.Background(), fakeClient, obj)
	if err != nil {
		t.Fatalf("Prepare() returned unexpected error: %v", err)
	}

	// After Prepare, annotations map should exist and have active-engine set
	if obj.Annotations == nil {
		t.Fatal("expected annotations map to be initialized, got nil")
	}
	if got := obj.Annotations[api.ActiveEngineAnnotation]; got != api.EngineZalando {
		t.Errorf("active-engine annotation = %q, want %q", got, api.EngineZalando)
	}
}
