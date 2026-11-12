package action

import (
	"context"
	"fmt"
	"reflect"

	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RequireUID constrains an existing resource action to its checkpointed identity.
// It guards every execution-time read, and forbids create/delete of the pinned
// target. Update and Claim retain their resourceVersion-based write protection.
// This does not implement resource replacement: identity loss fails closed.
func RequireUID(a Action, uid types.UID) Action {
	return &requireUID{Action: a, uid: uid}
}

type requireUID struct {
	Action
	uid types.UID
}

func (a *requireUID) SkipOwnership() bool {
	if policy, ok := a.Action.(interface{ SkipOwnership() bool }); ok {
		return policy.SkipOwnership()
	}
	return false
}

func (a *requireUID) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme, owners ownership.OwnerManager) error {
	if a.uid == "" {
		return fmt.Errorf("checkpointed action requires a non-empty UID")
	}
	guard := &uidClient{Client: c, target: a.GetObject(), uid: a.uid}
	return a.Action.Do(ctx, guard, scheme, owners)
}

type uidClient struct {
	client.Client
	target client.Object
	uid    types.UID
}

func (c *uidClient) matches(obj client.Object) bool {
	return reflect.TypeOf(obj) == reflect.TypeOf(c.target) && client.ObjectKeyFromObject(obj) == client.ObjectKeyFromObject(c.target)
}

func (c *uidClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := c.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if c.matches(obj) && (obj.GetUID() != c.uid || !obj.GetDeletionTimestamp().IsZero()) {
		return fmt.Errorf("checkpointed resource %s was replaced or is deleting (UID %q, expected %q)", key, obj.GetUID(), c.uid)
	}
	return nil
}

func (c *uidClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if c.matches(obj) {
		return fmt.Errorf("refusing to recreate checkpointed resource %s", client.ObjectKeyFromObject(obj))
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c *uidClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if c.matches(obj) {
		return fmt.Errorf("refusing to delete checkpointed resource %s during update", client.ObjectKeyFromObject(obj))
	}
	return c.Client.Delete(ctx, obj, opts...)
}
