package relatedobjectsmap

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type relatedKey struct {
	schema.GroupVersionKind
	types.NamespacedName
}

type RelatedObjectsMap struct {
	scheme  *runtime.Scheme
	objects map[relatedKey]client.Object
}

func NewRelatedObjectsMap(scheme *runtime.Scheme) *RelatedObjectsMap {
	return &RelatedObjectsMap{
		scheme:  scheme,
		objects: make(map[relatedKey]client.Object),
	}
}

func (r *RelatedObjectsMap) Insert(obj client.Object) {
	gvk, err := apiutil.GVKForObject(obj, r.scheme)
	if err != nil {
		panic(fmt.Sprintf("Programmer Error: Unable to find GVK for object %v: %v", obj, err))
	}
	key := relatedKey{
		GroupVersionKind: gvk,
		NamespacedName:   client.ObjectKeyFromObject(obj),
	}
	r.objects[key] = obj
}

func (r *RelatedObjectsMap) GetMatching(obj client.Object) client.Object {
	gvk, err := apiutil.GVKForObject(obj, r.scheme)
	if err != nil {
		panic(fmt.Sprintf("Programmer Error: Unable to find GVK for object %v: %v", obj, err))
	}
	key := relatedKey{
		GroupVersionKind: gvk,
		NamespacedName:   client.ObjectKeyFromObject(obj),
	}
	return r.objects[key]
}

func (r *RelatedObjectsMap) GetMatchingType(obj client.Object) []client.Object {
	gvk, err := apiutil.GVKForObject(obj, r.scheme)
	if err != nil {
		panic(fmt.Sprintf("Programmer Error: Unable to find GVK for object %v: %v", obj, err))
	}

	objects := make([]client.Object, 0)
	for key, object := range r.objects {
		if key.GroupKind() == gvk.GroupKind() {
			objects = append(objects, object)
		}
	}
	return objects
}
