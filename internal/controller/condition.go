package controller

import (
	"fmt"

	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const ConditionReasonUnknown = "Unknown"

func makeCondition(value bool) meta_v1.ConditionStatus {
	if value {
		return meta_v1.ConditionTrue
	}
	return meta_v1.ConditionFalse
}

func makeReason(condition *meta_v1.Condition) string {
	if condition == nil || condition.Reason == "" {
		return ConditionReasonUnknown
	}
	return condition.Reason
}

func makeMessage(condition *meta_v1.Condition) string {
	if condition == nil {
		return ""
	}
	return condition.Message
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
