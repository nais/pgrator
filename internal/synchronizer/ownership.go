package synchronizer

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func makeOwnerAnnotation(obj client.Object) string {
	return client.ObjectKeyFromObject(obj).String()
}

func (s *Synchronizer[T, P]) HasOwnerAnnotation(obj, owner client.Object) bool {
	ownerAnnotation := makeOwnerAnnotation(owner)
	for _, ownerReference := range s.GetOwnerAnnotations(obj) {
		if ownerReference == ownerAnnotation {
			return true
		}
	}
	return false
}

func (s *Synchronizer[T, P]) addOwnerAnnotation(obj client.Object, owner client.Object) {
	if s.HasOwnerAnnotation(obj, owner) {
		return
	}
	ownerAnnotation := makeOwnerAnnotation(owner)
	ownerReferences := s.GetOwnerAnnotations(obj)
	ownerReferences = append(ownerReferences, ownerAnnotation)
	s.setOwnerAnnotations(obj, ownerReferences)
}

func (s *Synchronizer[T, P]) removeOwnerAnnotation(obj client.Object, owner client.Object) {
	ownerAnnotation := makeOwnerAnnotation(owner)
	ownerReferences := s.GetOwnerAnnotations(obj)
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
	s.setOwnerAnnotations(obj, newOwnerReferences)
}

func (s *Synchronizer[T, P]) GetOwnerAnnotations(obj client.Object) []string {
	annotations := obj.GetAnnotations()
	if ownerAnnotations, ok := annotations[s.ownerAnnotationKey]; ok {
		if ownerAnnotations == "" {
			return []string{}
		}
		return strings.Split(ownerAnnotations, ",")
	}
	return []string{}
}

func (s *Synchronizer[T, P]) setOwnerAnnotations(obj client.Object, ownerReferences []string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	if len(ownerReferences) == 0 {
		delete(annotations, s.ownerAnnotationKey)
	} else {
		annotations[s.ownerAnnotationKey] = strings.Join(ownerReferences, ",")
	}
	obj.SetAnnotations(annotations)
}

func parseOwnerAnnotation(input string) (types.NamespacedName, error) {
	parts := strings.Split(input, string(types.Separator))
	if len(parts) != 2 {
		return types.NamespacedName{}, fmt.Errorf("can not parse invalid NamespacedName, incorrect number of parts: %d", len(parts))
	}
	return types.NamespacedName{
		Namespace: parts[0],
		Name:      parts[1],
	}, nil
}
