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
