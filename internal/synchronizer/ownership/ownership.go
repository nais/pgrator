package ownership

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type OwnerManager interface {
	HasOwnerAnnotation(obj, owner client.Object) bool
	AddOwnerAnnotation(obj client.Object, owner client.Object)
	RemoveOwnerAnnotation(obj client.Object, owner client.Object)
	GetOwnerAnnotations(obj client.Object) []string
	SetOwnerAnnotations(obj client.Object, ownerReferences []string)
}

type ownerManager struct {
	ownerAnnotationKey string
}

func NewOwnerManager(ownerAnnotationKey string) OwnerManager {
	return &ownerManager{ownerAnnotationKey: ownerAnnotationKey}
}

func makeOwnerAnnotation(obj client.Object) string {
	return client.ObjectKeyFromObject(obj).String()
}

func (o *ownerManager) HasOwnerAnnotation(obj, owner client.Object) bool {
	ownerAnnotation := makeOwnerAnnotation(owner)
	for _, ownerReference := range o.GetOwnerAnnotations(obj) {
		if ownerReference == ownerAnnotation {
			return true
		}
	}
	return false
}

func (o *ownerManager) AddOwnerAnnotation(obj client.Object, owner client.Object) {
	if o.HasOwnerAnnotation(obj, owner) {
		return
	}
	ownerAnnotation := makeOwnerAnnotation(owner)
	ownerReferences := o.GetOwnerAnnotations(obj)
	ownerReferences = append(ownerReferences, ownerAnnotation)
	o.SetOwnerAnnotations(obj, ownerReferences)
}

func (o *ownerManager) RemoveOwnerAnnotation(obj client.Object, owner client.Object) {
	ownerAnnotation := makeOwnerAnnotation(owner)
	ownerReferences := o.GetOwnerAnnotations(obj)
	newOwnerReferences := make([]string, 0, len(ownerReferences))
	found := false
	for _, ownerReference := range ownerReferences {
		if ownerReference == ownerAnnotation {
			found = true
			continue
		}
		newOwnerReferences = append(newOwnerReferences, ownerReference)
	}
	if !found {
		return
	}
	o.SetOwnerAnnotations(obj, newOwnerReferences)
}

func (o *ownerManager) GetOwnerAnnotations(obj client.Object) []string {
	annotations := obj.GetAnnotations()
	if ownerAnnotations, ok := annotations[o.ownerAnnotationKey]; ok {
		if ownerAnnotations == "" {
			return []string{}
		}
		return strings.Split(ownerAnnotations, ",")
	}
	return []string{}
}

func (o *ownerManager) SetOwnerAnnotations(obj client.Object, ownerReferences []string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	if len(ownerReferences) == 0 {
		delete(annotations, o.ownerAnnotationKey)
	} else {
		annotations[o.ownerAnnotationKey] = strings.Join(ownerReferences, ",")
	}
	obj.SetAnnotations(annotations)
}

func ParseOwnerAnnotation(input string) (types.NamespacedName, error) {
	parts := strings.Split(input, string(types.Separator))
	if len(parts) != 2 {
		return types.NamespacedName{}, fmt.Errorf("can not parse invalid NamespacedName, incorrect number of parts: %d", len(parts))
	}
	return types.NamespacedName{
		Namespace: parts[0],
		Name:      parts[1],
	}, nil
}
