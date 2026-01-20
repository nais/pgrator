package controller

import (
	"testing"
	"time"

	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/pkg/api/thirdparty/google/v1beta1"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// fillRequiredConditionFields sets required fields that are normally set by the controller
func fillRequiredConditionFields(condition *metav1.Condition) {
	if condition.LastTransitionTime.IsZero() {
		condition.LastTransitionTime = metav1.NewTime(time.Now())
	}
	if condition.Reason == "" {
		condition.Reason = "Unknown"
	}
}

func TestIAMConditionGetterProducesValidConditionTypes(t *testing.T) {
	tests := []struct {
		name string
		obj  *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember
	}{
		{
			name: "simple name",
			obj: &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{
				TypeMeta: metav1.TypeMeta{
					Kind:       "IAMPolicyMember",
					APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:       "outbox-wi-user",
					Namespace:  "org",
					Generation: 1,
				},
			},
		},
		{
			name: "name with dots",
			obj: &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{
				TypeMeta: metav1.TypeMeta{
					Kind:       "IAMPolicyMember",
					APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:       "my.resource.name",
					Namespace:  "org",
					Generation: 1,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conditions := iamConditionGetter(tt.obj)

			if len(conditions) == 0 {
				t.Fatal("expected at least one condition")
			}

			for _, condition := range conditions {
				fillRequiredConditionFields(&condition)
				errs := validation.ValidateCondition(condition, field.NewPath("status", "conditions"))
				if len(errs) > 0 {
					t.Errorf("condition type %q is invalid: %v", condition.Type, errs)
				}
			}
		})
	}
}

func TestIAMServiceAccountConditionGetterProducesValidConditionTypes(t *testing.T) {
	obj := &iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "IAMServiceAccount",
			APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "outbox",
			Namespace:  "org",
			Generation: 1,
		},
	}

	conditions := iamConditionGetter(obj)

	if len(conditions) == 0 {
		t.Fatal("expected at least one condition")
	}

	for _, condition := range conditions {
		fillRequiredConditionFields(&condition)
		errs := validation.ValidateCondition(condition, field.NewPath("status", "conditions"))
		if len(errs) > 0 {
			t.Errorf("condition type %q is invalid: %v", condition.Type, errs)
		}
	}
}

func TestExistsConditionGetterProducesValidConditionTypes(t *testing.T) {
	obj := &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{
		TypeMeta: metav1.TypeMeta{
			Kind:       "IAMPolicyMember",
			APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-resource",
			Namespace:  "org",
			Generation: 1,
		},
	}

	conditions := existsConditionGetter(obj)

	if len(conditions) == 0 {
		t.Fatal("expected at least one condition")
	}

	for _, condition := range conditions {
		fillRequiredConditionFields(&condition)
		errs := validation.ValidateCondition(condition, field.NewPath("status", "conditions"))
		if len(errs) > 0 {
			t.Errorf("condition type %q is invalid: %v", condition.Type, errs)
		}
	}
}

func TestPostgresqlConditionGetterProducesValidConditionTypes(t *testing.T) {
	obj := &acid_zalan_do_v1.Postgresql{
		TypeMeta: metav1.TypeMeta{
			Kind:       "postgresql",
			APIVersion: "acid.zalan.do/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "pg-team",
			Generation: 1,
		},
		Status: acid_zalan_do_v1.PostgresStatus{
			PostgresClusterStatus: acid_zalan_do_v1.ClusterStatusRunning,
		},
	}

	conditions := postgresqlConditionGetter(obj)

	if len(conditions) == 0 {
		t.Fatal("expected at least one condition")
	}

	for _, condition := range conditions {
		fillRequiredConditionFields(&condition)
		errs := validation.ValidateCondition(condition, field.NewPath("status", "conditions"))
		if len(errs) > 0 {
			t.Errorf("condition type %q is invalid: %v", condition.Type, errs)
		}
	}
}
