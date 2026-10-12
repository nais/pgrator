package action

import (
	"encoding/json"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Store field ownership, never values: children can contain credentials.
const managedFieldsAnnotation = "pgrator.nais.io/managed-fields"

func prepareCreate(obj client.Object) error {
	content, err := managedContent(obj)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(fieldTree(content))
	if err != nil {
		return err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[managedFieldsAnnotation] = string(encoded)
	obj.SetAnnotations(annotations)
	return nil
}

// prepareUpdate merges desired fields into the live object and removes fields
// managed on the previous pass that are no longer desired. Maps merge recursively;
// lists are atomic. Status and server metadata always come from the live object.
// Existing resources without a baseline are adopted using the old replacement
// contract. New resources record their baseline before admission runs.
func prepareUpdate(desired, existing client.Object) (bool, error) {
	if existing.GetDeletionTimestamp() != nil {
		return false, fmt.Errorf("%s is being deleted", describeObj(existing))
	}
	wanted, err := managedContent(desired)
	if err != nil {
		return false, err
	}
	current, err := runtime.DefaultUnstructuredConverter.ToUnstructured(existing)
	if err != nil {
		return false, err
	}
	if _, ok := desired.(*corev1.Secret); ok {
		delete(current, "stringData")
	}
	previous := map[string]any{}
	if encoded := existing.GetAnnotations()[managedFieldsAnnotation]; encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &previous); err != nil {
			return false, fmt.Errorf("reading managed fields: %w", err)
		}
	} else {
		old, err := managedContent(existing)
		if err != nil {
			return false, err
		}
		previous = fieldTree(old)
	}
	mergeManaged(current, wanted, previous)
	encoded, err := json.Marshal(fieldTree(wanted))
	if err != nil {
		return false, err
	}
	metadata := current["metadata"].(map[string]any)
	annotations, _ := metadata["annotations"].(map[string]any)
	if annotations == nil {
		annotations = map[string]any{}
		metadata["annotations"] = annotations
	}
	annotations[managedFieldsAnnotation] = string(encoded)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(current, desired); err != nil {
		return false, err
	}
	return !equality.Semantic.DeepEqual(existing, desired), nil
}

func managedContent(obj client.Object) (map[string]any, error) {
	obj = obj.DeepCopyObject().(client.Object)
	// NetworkPolicy rules are atomic lists. Normalize their API default before
	// comparison so an omitted protocol does not fight the server's TCP default.
	if policy, ok := obj.(*networkingv1.NetworkPolicy); ok {
		defaultPorts := func(ports []networkingv1.NetworkPolicyPort) {
			for i := range ports {
				if ports[i].Protocol == nil {
					ports[i].Protocol = new(corev1.ProtocolTCP)
				}
			}
		}
		for i := range policy.Spec.Ingress {
			defaultPorts(policy.Spec.Ingress[i].Ports)
		}
		for i := range policy.Spec.Egress {
			defaultPorts(policy.Spec.Egress[i].Ports)
		}
	}
	// StringData is write-only; compare the equivalent persisted Data instead.
	if secret, ok := obj.(*corev1.Secret); ok && len(secret.StringData) > 0 {
		copy := secret.DeepCopy()
		if copy.Data == nil {
			copy.Data = map[string][]byte{}
		}
		for key, value := range copy.StringData {
			copy.Data[key] = []byte(value)
		}
		copy.StringData = nil
		obj = copy
	}
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	// False is authoritative for role privileges, even though CNPG's Go types
	// omit false values from JSON. An unexpected grant must be reconciled away.
	if role, ok := obj.(*cnpgv1.DatabaseRole); ok {
		spec := content["spec"].(map[string]any)
		r := role.Spec.RoleConfiguration
		for key, value := range map[string]bool{
			"login": r.Login, "superuser": r.Superuser, "createdb": r.CreateDB,
			"createrole": r.CreateRole, "replication": r.Replication,
			"bypassrls": r.BypassRLS, "disablePassword": r.DisablePassword,
		} {
			spec[key] = value
		}
	}
	delete(content, "status")
	delete(content, "apiVersion")
	delete(content, "kind")
	metadata := map[string]any{}
	for _, key := range []string{"labels", "annotations", "ownerReferences", "finalizers"} {
		if value, ok := content["metadata"].(map[string]any)[key]; ok {
			metadata[key] = value
		}
	}
	if annotations, ok := metadata["annotations"].(map[string]any); ok {
		delete(annotations, managedFieldsAnnotation)
		if len(annotations) == 0 {
			delete(metadata, "annotations")
		}
	}
	content["metadata"] = metadata
	return content, nil
}

func fieldTree(content map[string]any) map[string]any {
	tree := make(map[string]any, len(content))
	for key, value := range content {
		if nested, ok := value.(map[string]any); ok {
			tree[key] = fieldTree(nested)
		} else {
			tree[key] = true
		}
	}
	return tree
}

func mergeManaged(current, desired, previous map[string]any) {
	for key := range previous {
		if _, ok := desired[key]; ok {
			continue
		}
		delete(current, key)
	}
	for key, value := range desired {
		if nested, ok := value.(map[string]any); ok {
			live, _ := current[key].(map[string]any)
			if live == nil {
				live = map[string]any{}
				current[key] = live
			}
			old, _ := previous[key].(map[string]any)
			mergeManaged(live, nested, old)
		} else {
			current[key] = value
		}
	}
}
