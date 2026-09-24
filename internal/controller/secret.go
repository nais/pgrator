package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// readSecretData retrieves the specified keys from a Secret. If the Secret does
// not exist or any requested key is missing/empty, it returns ok=false.
func readSecretData(ctx context.Context, reader client.Reader, key client.ObjectKey, keys ...string) (map[string][]byte, bool, error) {
	secret := &corev1.Secret{}
	if err := reader.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("getting Secret %q: %w", key.Name, err)
	}
	data := make(map[string][]byte, len(keys))
	for _, key := range keys {
		value, ok := secret.Data[key]
		if !ok || len(value) == 0 {
			return nil, false, nil
		}
		data[key] = value
	}
	return data, true, nil
}
