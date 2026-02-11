package controller

import (
	"fmt"
	"strings"

	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

const ConditionReasonUnknown = "Unknown"

func makeCondition(value bool) meta_v1.ConditionStatus {
	if value {
		return meta_v1.ConditionTrue
	}
	return meta_v1.ConditionFalse
}

// conditionsEqual returns true if two condition slices have the same content.
func conditionsEqual(a, b []meta_v1.Condition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type ||
			a[i].Status != b[i].Status ||
			a[i].Reason != b[i].Reason ||
			a[i].Message != b[i].Message ||
			!a[i].LastTransitionTime.Equal(&b[i].LastTransitionTime) {
			return false
		}
	}
	return true
}

func typePrefix(obj client.Object, scheme *runtime.Scheme) string {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		panic(fmt.Sprintf("Programming error: get GVK for object: %v", err))
	}
	return strings.ToLower(gvk.GroupKind().String())
}

func existsConditionGetter(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
	return []meta_v1.Condition{
		{
			Type:               fmt.Sprintf("%s/Available", typePrefix(obj, scheme)),
			Status:             makeCondition(obj != nil),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             "Exists",
		},
	}
}
