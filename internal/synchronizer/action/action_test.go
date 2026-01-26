package action

import (
	"context"

	"github.com/nais/pgrator/internal/synchronizer/object"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	core_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockRecorder struct{}

func (m *mockRecorder) RecordEvent(obj object.NaisObject, eventType, reason, messageFmt string, args ...any) {
	// Mock implementation - does nothing
}

func (m *mockRecorder) RecordErrorEvent(obj object.NaisObject, phase string, err error) {
	// Mock implementation - does nothing
}

var _ = Describe("CreateOrRecreate Action", func() {
	var (
		scheme          *runtime.Scheme
		fakeClient      client.Client
		postgres        *data_nais_io_v1.Postgres
		recorder        *mockRecorder
		conditionGetter ConditionGetter
		ctx             context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(core_v1.AddToScheme(scheme)).To(Succeed())
		Expect(data_nais_io_v1.AddToScheme(scheme)).To(Succeed())

		postgres = &data_nais_io_v1.Postgres{
			ObjectMeta: meta_v1.ObjectMeta{
				Name:      "test-postgres",
				Namespace: "test-namespace",
			},
			Spec: data_nais_io_v1.PostgresSpec{},
		}
		postgres.Status = &data_nais_io_v1.PostgresStatus{}

		recorder = &mockRecorder{}

		conditionGetter = func(obj client.Object) []meta_v1.Condition {
			return []meta_v1.Condition{
				{
					Type:   "serviceaccount/Available",
					Status: meta_v1.ConditionTrue,
					Reason: "Exists",
				},
			}
		}
	})

	Context("when resource does not exist", func() {
		BeforeEach(func() {
			fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
		})

		It("should create the resource", func() {
			serviceAccount := &core_v1.ServiceAccount{
				TypeMeta: meta_v1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: "v1",
				},
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa",
					Namespace: "test-namespace",
				},
			}

			action := Recreate(serviceAccount, postgres, conditionGetter, recorder)
			err := action.Do(ctx, fakeClient, scheme)
			Expect(err).NotTo(HaveOccurred())

			var created core_v1.ServiceAccount
			err = fakeClient.Get(ctx, client.ObjectKey{Name: "test-sa", Namespace: "test-namespace"}, &created)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("when resource already exists", func() {
		It("should delete and recreate the resource, clearing all old metadata", func() {
			existingServiceAccount := &core_v1.ServiceAccount{
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"old-label": "old-value",
					},
					Annotations: map[string]string{
						"creation-timestamp": "2024-01-01",
						"old-annotation":     "should-be-removed",
					},
				},
			}

			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(existingServiceAccount).
				Build()

			newServiceAccount := &core_v1.ServiceAccount{
				TypeMeta: meta_v1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: "v1",
				},
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"new-label": "new-value",
					},
				},
			}

			action := Recreate(newServiceAccount, postgres, conditionGetter, recorder)
			err := action.Do(ctx, fakeClient, scheme)
			Expect(err).NotTo(HaveOccurred())

			var recreated core_v1.ServiceAccount
			err = fakeClient.Get(ctx, client.ObjectKey{Name: "test-sa", Namespace: "test-namespace"}, &recreated)
			Expect(err).NotTo(HaveOccurred())

			// CRITICAL: Old annotations/labels should be COMPLETELY gone (not merged)
			// This proves Delete+Create happened, not Update
			By("verifying old annotation is gone")
			_, hasOldAnnotation := recreated.Annotations["creation-timestamp"]
			Expect(hasOldAnnotation).To(BeFalse(), "Old annotation should be cleared by recreation (Delete+Create), not merged")

			By("verifying old label is gone")
			_, hasOldLabel := recreated.Labels["old-label"]
			Expect(hasOldLabel).To(BeFalse(), "Old label should be cleared by recreation (Delete+Create), not merged")

			By("verifying new label is present")
			Expect(recreated.Labels["new-label"]).To(Equal("new-value"))

			By("verifying exactly one label exists")
			Expect(recreated.Labels).To(HaveLen(1), "Should have exactly 1 label after recreation")
		})
	})

	Describe("comparison with CreateOrUpdate", func() {
		It("should behave differently from CreateOrUpdate by clearing old metadata", func() {
			// First, test CreateOrUpdate behavior for comparison
			existingForUpdate := &core_v1.ServiceAccount{
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa-update",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"external-state": "should-remain-with-update",
					},
				},
			}

			fakeClient = fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(existingForUpdate).
				Build()

			updateServiceAccount := &core_v1.ServiceAccount{
				TypeMeta: meta_v1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: "v1",
				},
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa-update",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"new-annotation": "added-by-update",
					},
				},
			}

			updateAction := CreateOrUpdate(updateServiceAccount, postgres, conditionGetter, recorder)
			err := updateAction.Do(ctx, fakeClient, scheme)
			Expect(err).NotTo(HaveOccurred())

			// Now test CreateOrRecreate with a separate resource
			existingForRecreate := &core_v1.ServiceAccount{
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa-recreate",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"external-state":     "should-be-cleared",
						"another-annotation": "also-should-be-cleared",
					},
					Labels: map[string]string{
						"old-label": "old-value",
					},
				},
			}

			err = fakeClient.Create(ctx, existingForRecreate)
			Expect(err).NotTo(HaveOccurred())

			newRecreateServiceAccount := &core_v1.ServiceAccount{
				TypeMeta: meta_v1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: "v1",
				},
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa-recreate",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"recreate-annotation": "added-by-recreate",
					},
					Labels: map[string]string{
						"new-label": "new-value",
					},
				},
			}

			recreateAction := Recreate(newRecreateServiceAccount, postgres, conditionGetter, recorder)
			err = recreateAction.Do(ctx, fakeClient, scheme)
			Expect(err).NotTo(HaveOccurred())

			var afterRecreate core_v1.ServiceAccount
			err = fakeClient.Get(ctx, client.ObjectKey{Name: "test-sa-recreate", Namespace: "test-namespace"}, &afterRecreate)
			Expect(err).NotTo(HaveOccurred())

			// CRITICAL: Old annotations should be COMPLETELY gone (not merged)
			By("verifying all old annotations are cleared")
			_, hasExternalState := afterRecreate.Annotations["external-state"]
			Expect(hasExternalState).To(BeFalse(), "CreateOrRecreate should DELETE old resource, not UPDATE it")

			_, hasAnotherAnnotation := afterRecreate.Annotations["another-annotation"]
			Expect(hasAnotherAnnotation).To(BeFalse(), "CreateOrRecreate should DELETE old resource, not UPDATE it")

			By("verifying old labels are cleared")
			_, hasOldLabel := afterRecreate.Labels["old-label"]
			Expect(hasOldLabel).To(BeFalse(), "CreateOrRecreate should DELETE old resource, not UPDATE it")

			By("verifying only new metadata exists")
			Expect(afterRecreate.Annotations["recreate-annotation"]).To(Equal("added-by-recreate"))
			Expect(afterRecreate.Labels["new-label"]).To(Equal("new-value"))
			Expect(afterRecreate.Annotations).To(HaveLen(1), "Should have exactly 1 annotation")
			Expect(afterRecreate.Labels).To(HaveLen(1), "Should have exactly 1 label")
		})
	})

	// Table-driven test for metadata clearing scenarios
	Describe("metadata clearing behavior", func() {
		type metadataTestCase struct {
			description      string
			oldAnnotations   map[string]string
			oldLabels        map[string]string
			newAnnotations   map[string]string
			newLabels        map[string]string
			expectOldCleared bool
		}

		DescribeTable("should handle various metadata scenarios",
			func(tc metadataTestCase) {
				existing := &core_v1.ServiceAccount{
					ObjectMeta: meta_v1.ObjectMeta{
						Name:        "test-sa-table",
						Namespace:   "test-namespace",
						Annotations: tc.oldAnnotations,
						Labels:      tc.oldLabels,
					},
				}

				fakeClient = fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(existing).
					Build()

				newSA := &core_v1.ServiceAccount{
					TypeMeta: meta_v1.TypeMeta{
						Kind:       "ServiceAccount",
						APIVersion: "v1",
					},
					ObjectMeta: meta_v1.ObjectMeta{
						Name:        "test-sa-table",
						Namespace:   "test-namespace",
						Annotations: tc.newAnnotations,
						Labels:      tc.newLabels,
					},
				}

				action := Recreate(newSA, postgres, conditionGetter, recorder)
				err := action.Do(ctx, fakeClient, scheme)
				Expect(err).NotTo(HaveOccurred())

				var result core_v1.ServiceAccount
				err = fakeClient.Get(ctx, client.ObjectKey{Name: "test-sa-table", Namespace: "test-namespace"}, &result)
				Expect(err).NotTo(HaveOccurred())

				if tc.expectOldCleared {
					// Verify old annotations are gone
					for key := range tc.oldAnnotations {
						_, exists := result.Annotations[key]
						Expect(exists).To(BeFalse(), "Old annotation '%s' should be cleared", key)
					}
					// Verify old labels are gone
					for key := range tc.oldLabels {
						_, exists := result.Labels[key]
						Expect(exists).To(BeFalse(), "Old label '%s' should be cleared", key)
					}
				}

				// Verify new annotations are present
				for key, value := range tc.newAnnotations {
					Expect(result.Annotations[key]).To(Equal(value))
				}
				// Verify new labels are present
				for key, value := range tc.newLabels {
					Expect(result.Labels[key]).To(Equal(value))
				}
			},
			Entry("single old annotation should be cleared", metadataTestCase{
				description:      "single old annotation cleared",
				oldAnnotations:   map[string]string{"old": "value"},
				oldLabels:        map[string]string{},
				newAnnotations:   map[string]string{"new": "value"},
				newLabels:        map[string]string{},
				expectOldCleared: true,
			}),
			Entry("multiple old annotations should be cleared", metadataTestCase{
				description:      "multiple old annotations cleared",
				oldAnnotations:   map[string]string{"old1": "value1", "old2": "value2"},
				oldLabels:        map[string]string{},
				newAnnotations:   map[string]string{"new": "value"},
				newLabels:        map[string]string{},
				expectOldCleared: true,
			}),
			Entry("old labels and annotations should both be cleared", metadataTestCase{
				description:      "both labels and annotations cleared",
				oldAnnotations:   map[string]string{"old-annotation": "value"},
				oldLabels:        map[string]string{"old-label": "value"},
				newAnnotations:   map[string]string{"new-annotation": "value"},
				newLabels:        map[string]string{"new-label": "value"},
				expectOldCleared: true,
			}),
			Entry("empty old metadata should work", metadataTestCase{
				description:      "empty old metadata",
				oldAnnotations:   map[string]string{},
				oldLabels:        map[string]string{},
				newAnnotations:   map[string]string{"new": "value"},
				newLabels:        map[string]string{"label": "value"},
				expectOldCleared: true,
			}),
			Entry("complex scenario with many fields", metadataTestCase{
				description: "complex metadata scenario",
				oldAnnotations: map[string]string{
					"external-state": "should-clear",
					"timestamp":      "2024-01-01",
					"version":        "v1",
				},
				oldLabels: map[string]string{
					"env":  "prod",
					"team": "platform",
				},
				newAnnotations: map[string]string{
					"iam.gke.io/gcp-service-account": "sa@project.iam.gserviceaccount.com",
				},
				newLabels: map[string]string{
					"app": "postgres",
				},
				expectOldCleared: true,
			}),
		)
	})

	Context("GVK preservation", func() {
		var testConditionGetter ConditionGetter
		var seenGVK string

		BeforeEach(func() {
			fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()
			seenGVK = ""
			// Track what GVK the condition getter sees
			// This simulates the real condition getters that rely on GVK being set
			testConditionGetter = func(obj client.Object) []meta_v1.Condition {
				gvk := obj.GetObjectKind().GroupVersionKind()
				seenGVK = gvk.String()
				return []meta_v1.Condition{
					{
						Type:   "serviceaccount/Available",
						Status: meta_v1.ConditionTrue,
						Reason: "Exists",
					},
				}
			}
		})

		It("should preserve GVK in Recreate action", func() {
			serviceAccount := &core_v1.ServiceAccount{
				TypeMeta: meta_v1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: "v1",
				},
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa",
					Namespace: "test-namespace",
				},
			}

			action := Recreate(serviceAccount, postgres, testConditionGetter, recorder)
			err := action.Do(ctx, fakeClient, scheme)
			Expect(err).NotTo(HaveOccurred())
			Expect(seenGVK).To(Equal("/v1, Kind=ServiceAccount"))
		})

		It("should preserve GVK in Create action", func() {
			serviceAccount := &core_v1.ServiceAccount{
				TypeMeta: meta_v1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: "v1",
				},
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa-create",
					Namespace: "test-namespace",
				},
			}

			action := Create(serviceAccount, postgres, testConditionGetter, recorder)
			err := action.Do(ctx, fakeClient, scheme)
			Expect(err).NotTo(HaveOccurred())
			Expect(seenGVK).To(Equal("/v1, Kind=ServiceAccount"))
		})

		It("should preserve GVK in CreateIfNotExists action (create path)", func() {
			serviceAccount := &core_v1.ServiceAccount{
				TypeMeta: meta_v1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: "v1",
				},
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa-cine",
					Namespace: "test-namespace",
				},
			}

			action := CreateIfNotExists(serviceAccount, postgres, testConditionGetter, recorder)
			err := action.Do(ctx, fakeClient, scheme)
			Expect(err).NotTo(HaveOccurred())
			Expect(seenGVK).To(Equal("/v1, Kind=ServiceAccount"))
		})

		It("should preserve GVK in CreateIfNotExists action (exists path)", func() {
			// Pre-create the resource
			existing := &core_v1.ServiceAccount{
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa-exists",
					Namespace: "test-namespace",
				},
			}
			err := fakeClient.Create(ctx, existing)
			Expect(err).NotTo(HaveOccurred())

			// Now try to create it again with CreateIfNotExists
			serviceAccount := &core_v1.ServiceAccount{
				TypeMeta: meta_v1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: "v1",
				},
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa-exists",
					Namespace: "test-namespace",
				},
			}

			action := CreateIfNotExists(serviceAccount, postgres, testConditionGetter, recorder)
			err = action.Do(ctx, fakeClient, scheme)
			Expect(err).NotTo(HaveOccurred())
			Expect(seenGVK).To(Equal("/v1, Kind=ServiceAccount"))
		})

		It("should preserve GVK in CreateOrUpdate action (update path)", func() {
			// Pre-create the resource
			existing := &core_v1.ServiceAccount{
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa-update",
					Namespace: "test-namespace",
				},
			}
			err := fakeClient.Create(ctx, existing)
			Expect(err).NotTo(HaveOccurred())

			// Now update it with CreateOrUpdate
			serviceAccount := &core_v1.ServiceAccount{
				TypeMeta: meta_v1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: "v1",
				},
				ObjectMeta: meta_v1.ObjectMeta{
					Name:      "test-sa-update",
					Namespace: "test-namespace",
					Labels: map[string]string{
						"updated": "true",
					},
				},
			}

			action := CreateOrUpdate(serviceAccount, postgres, testConditionGetter, recorder)
			err = action.Do(ctx, fakeClient, scheme)
			Expect(err).NotTo(HaveOccurred())
			Expect(seenGVK).To(Equal("/v1, Kind=ServiceAccount"))
		})
	})
})
