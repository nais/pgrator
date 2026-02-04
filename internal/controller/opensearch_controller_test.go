package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/controller/resourcecreator"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	aiven_v1alpha1 "github.com/nais/pgrator/internal/thirdparty/aiven/v1alpha1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
)

var (
	_ reconciler.Reconciler[*v1.OpenSearch, OpenSearchPreparedData] = &OpenSearchReconciler{}
	_ reconciler.FinalizerNamer                                     = &OpenSearchReconciler{}
)

const stateRunning = "RUNNING"

var _ = Describe("OpenSearch Controller", func() {
	Describe("CreateAivenOpenSearchSpec", func() {
		const (
			testOpenSearchName = "my-opensearch"
			testTeamName       = "my-team"
		)
		aiven := config.Aiven{
			Project:      "test-project",
			ProjectVPCID: "vpc-123",
		}
		tenant := config.Tenant{
			Name: "test-tenant",
		}

		It("should create basic opensearch with correct fields", func() {
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testOpenSearchName,
					Namespace: testTeamName,
					Labels: map[string]string{
						"team": testTeamName,
					},
				},
				Spec: v1.OpenSearchSpec{
					Tier:      v1.OpenSearchTierSingleNode,
					Memory:    v1.OpenSearchMemory4GB,
					Version:   v1.OpenSearchVersionV2,
					StorageGB: 80,
				},
			}

			result, err := resourcecreator.CreateAivenOpenSearchSpec(scheme.Scheme, opensearch, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Spec.Project).To(Equal("test-project"))
			Expect(result.Spec.Plan).To(Equal("startup-4"))
			Expect(result.Spec.ProjectVPCID).To(Equal("vpc-123"))
			Expect(result.Spec.DiskSpace).To(Equal("80GiB"))
			Expect(result.Spec.TerminationProtection).NotTo(BeNil())
			Expect(*result.Spec.TerminationProtection).To(BeFalse())
			Expect(result.Spec.UserConfig).NotTo(BeNil())
			Expect(result.Spec.UserConfig.OpenSearchVersion).NotTo(BeNil())
			Expect(*result.Spec.UserConfig.OpenSearchVersion).To(Equal("2"))
			Expect(result.Spec.Tags["team"]).To(Equal(testTeamName))
			Expect(result.Spec.Tags["app"]).To(Equal(testOpenSearchName))
			Expect(result.Spec.Tags["tenant"]).To(Equal("test-tenant"))
			// Verify Aiven resource uses namespaced name
			Expect(result.Name).To(Equal("opensearch-" + testTeamName + "-" + testOpenSearchName))
		})

		It("should set correct version for V2_19", func() {
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "versioned-opensearch",
					Namespace: "version-team",
				},
				Spec: v1.OpenSearchSpec{
					Tier:      v1.OpenSearchTierSingleNode,
					Memory:    v1.OpenSearchMemory4GB,
					Version:   v1.OpenSearchVersionV2_19,
					StorageGB: 80,
				},
			}

			result, err := resourcecreator.CreateAivenOpenSearchSpec(scheme.Scheme, opensearch, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Spec.UserConfig).NotTo(BeNil())
			Expect(result.Spec.UserConfig.OpenSearchVersion).NotTo(BeNil())
			Expect(*result.Spec.UserConfig.OpenSearchVersion).To(Equal("2.19"))
		})

		It("should set correct version for V3_3", func() {
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "v3-opensearch",
					Namespace: "v3-team",
				},
				Spec: v1.OpenSearchSpec{
					Tier:      v1.OpenSearchTierHighAvailability,
					Memory:    v1.OpenSearchMemory8GB,
					Version:   v1.OpenSearchVersionV3_3,
					StorageGB: 525,
				},
			}

			result, err := resourcecreator.CreateAivenOpenSearchSpec(scheme.Scheme, opensearch, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Spec.Plan).To(Equal("business-8"))
			Expect(result.Spec.DiskSpace).To(Equal("525GiB"))
			Expect(result.Spec.UserConfig).NotTo(BeNil())
			Expect(result.Spec.UserConfig.OpenSearchVersion).NotTo(BeNil())
			Expect(*result.Spec.UserConfig.OpenSearchVersion).To(Equal("3.3"))
		})

		It("should create hobbyist plan for 2GB memory", func() {
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hobbyist-opensearch",
					Namespace: "dev-team",
				},
				Spec: v1.OpenSearchSpec{
					Tier:      v1.OpenSearchTierSingleNode,
					Memory:    v1.OpenSearchMemory2GB,
					Version:   v1.OpenSearchVersionV1,
					StorageGB: 16,
				},
			}

			result, err := resourcecreator.CreateAivenOpenSearchSpec(scheme.Scheme, opensearch, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Spec.Plan).To(Equal("hobbyist"))
			Expect(result.Spec.DiskSpace).To(Equal("16GiB"))
		})

		It("should preserve labels from source opensearch", func() {
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "labeled-opensearch",
					Namespace: "labeled-team",
					Labels: map[string]string{
						"team":         "labeled-team",
						"custom-label": "custom-value",
						"another":      "label",
					},
				},
				Spec: v1.OpenSearchSpec{
					Tier:      v1.OpenSearchTierSingleNode,
					Memory:    v1.OpenSearchMemory4GB,
					Version:   v1.OpenSearchVersionV2,
					StorageGB: 80,
				},
			}

			result, err := resourcecreator.CreateAivenOpenSearchSpec(scheme.Scheme, opensearch, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Labels["team"]).To(Equal("labeled-team"))
			Expect(result.Labels["custom-label"]).To(Equal("custom-value"))
			Expect(result.Labels["another"]).To(Equal("label"))
			Expect(result.Labels["opensearch.nais.io/name"]).To(Equal("labeled-opensearch"))
			Expect(result.Name).To(Equal("opensearch-labeled-team-labeled-opensearch"))
		})
	})

	Describe("CreateOpenSearchServiceIntegrationSpec", func() {
		It("should create service integration with correct fields", func() {
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-opensearch",
					Namespace: "my-team",
					Labels: map[string]string{
						"team": "my-team",
					},
				},
				Spec: v1.OpenSearchSpec{
					Tier:      v1.OpenSearchTierSingleNode,
					Memory:    v1.OpenSearchMemory4GB,
					Version:   v1.OpenSearchVersionV2,
					StorageGB: 80,
				},
			}
			cfg := config.Aiven{
				Project:                      "test-project",
				MetricsDestinationEndpointID: "metrics-service",
			}

			result, err := resourcecreator.CreateOpenSearchServiceIntegrationSpec(scheme.Scheme, opensearch, cfg)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Name).To(Equal("opensearch-my-team-my-opensearch"))
			Expect(result.Namespace).To(Equal("my-team"))
			Expect(result.Spec.Project).To(Equal("test-project"))
			Expect(result.Spec.IntegrationType).To(Equal("prometheus"))
			Expect(result.Spec.SourceServiceName).To(Equal("opensearch-my-team-my-opensearch"))
			Expect(result.Spec.DestinationEndpointID).To(Equal("metrics-service"))
		})
	})

	Describe("aivenOpenSearchConditionGetter", func() {
		It("should return ObservedState=True when state is non-empty", func() {
			aivenOpenSearch := &aiven_v1alpha1.OpenSearch{
				Status: aiven_v1alpha1.ServiceStatus{
					State: stateRunning,
				},
			}
			aivenOpenSearch.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("OpenSearch"))

			conditions := aivenOpenSearchConditionGetter(aivenOpenSearch, scheme.Scheme)

			Expect(conditions).To(HaveLen(1))
			observedState := meta.FindStatusCondition(conditions, "opensearch.aiven.io/ObservedState")
			Expect(observedState).NotTo(BeNil())
			Expect(observedState.Status).To(Equal(metav1.ConditionTrue))
			Expect(observedState.Reason).To(Equal("Reconciled"))
			Expect(observedState.Message).To(Equal("OpenSearch is in state: RUNNING"))
		})

		It("should return ObservedState=False when state is empty", func() {
			aivenOpenSearch := &aiven_v1alpha1.OpenSearch{
				Status: aiven_v1alpha1.ServiceStatus{
					State: "",
				},
			}
			aivenOpenSearch.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("OpenSearch"))

			conditions := aivenOpenSearchConditionGetter(aivenOpenSearch, scheme.Scheme)

			Expect(conditions).To(HaveLen(1))
			observedState := meta.FindStatusCondition(conditions, "opensearch.aiven.io/ObservedState")
			Expect(observedState).NotTo(BeNil())
			Expect(observedState.Status).To(Equal(metav1.ConditionFalse))
			Expect(observedState.Reason).To(Equal("Reconciled"))
			Expect(observedState.Message).To(Equal("OpenSearch is in state: "))
		})
	})

	Describe("openSearchServiceIntegrationConditionGetter", func() {
		It("should return nil", func() {
			integration := &aiven_v1alpha1.ServiceIntegration{
				Status: aiven_v1alpha1.ServiceIntegrationStatus{
					Conditions: []metav1.Condition{
						{
							Type:    stateRunning,
							Status:  metav1.ConditionTrue,
							Reason:  "CheckRunning",
							Message: "Integration is running",
						},
					},
				},
			}
			integration.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("ServiceIntegration"))

			conditions := openSearchServiceIntegrationConditionGetter(integration, scheme.Scheme)

			Expect(conditions).To(BeNil())
		})
	})

	Describe("OpenSearch state change reconciliation", func() {
		const (
			opensearchStateTestNamespace = "opensearch-state-test"
		)

		var controllerReconciler *synchronizer.Synchronizer[*v1.OpenSearch, OpenSearchPreparedData]

		BeforeEach(func() {
			By("using a fresh recorder to avoid blocking on full channel from previous tests")
			recorder := events.NewRecorder(record.NewFakeRecorder(100))

			By("creating the synchronizer for opensearch")
			opensearchReconciler := &OpenSearchReconciler{
				Aiven: config.Aiven{
					Project:                      "test-project",
					ProjectVPCID:                 "test-vpc-id",
					MetricsDestinationEndpointID: "test-metrics-service",
				},
				Tenant:   config.Tenant{Name: "test-tenant"},
				Recorder: recorder,
				Scheme:   k8sClient.Scheme(),
			}
			controllerReconciler = synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), opensearchReconciler, recorder)

			By("creating the test namespace")
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: opensearchStateTestNamespace,
				},
			}
			err := k8sClient.Get(context.Background(), types.NamespacedName{Name: opensearchStateTestNamespace}, ns)
			if apierrors.IsNotFound(err) {
				Expect(k8sClient.Create(context.Background(), ns)).To(Succeed())
			}
		})

		It("should update condition when Aiven OpenSearch state changes from empty to RUNNING", func() {
			opensearchName := "state-change-test"
			opensearchKey := types.NamespacedName{Name: opensearchName, Namespace: opensearchStateTestNamespace}
			aivenOpenSearchName := "opensearch-" + opensearchStateTestNamespace + "-" + opensearchName

			By("creating an OpenSearch resource")
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      opensearchName,
					Namespace: opensearchStateTestNamespace,
				},
				Spec: v1.OpenSearchSpec{
					Tier:      v1.OpenSearchTierSingleNode,
					Memory:    v1.OpenSearchMemory4GB,
					Version:   v1.OpenSearchVersionV2,
					StorageGB: 80,
				},
			}
			Expect(k8sClient.Create(context.Background(), opensearch)).To(Succeed())

			By("reconciling the OpenSearch resource (initial)")
			_, err := controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: opensearchKey})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Aiven OpenSearch was created")
			aivenOpenSearch := &aiven_v1alpha1.OpenSearch{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenOpenSearchName, Namespace: opensearchStateTestNamespace}, aivenOpenSearch)).To(Succeed())

			By("checking initial condition (state is empty)")
			Expect(k8sClient.Get(context.Background(), opensearchKey, opensearch)).To(Succeed())
			initialCondition := meta.FindStatusCondition(opensearch.GetStatus().GetConditions(), "opensearch.aiven.io/ObservedState")
			Expect(initialCondition).NotTo(BeNil())
			Expect(initialCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(initialCondition.Message).To(Equal("OpenSearch is in state: "))
			Expect(initialCondition.LastTransitionTime.IsZero()).To(BeFalse())

			By("simulating Aiven OpenSearch state change to RUNNING")
			aivenOpenSearch.Status.State = stateRunning
			Expect(k8sClient.Status().Update(context.Background(), aivenOpenSearch)).To(Succeed())

			By("reconciling the OpenSearch resource again")
			_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: opensearchKey})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the condition was updated with new state")
			Expect(k8sClient.Get(context.Background(), opensearchKey, opensearch)).To(Succeed())
			updatedCondition := meta.FindStatusCondition(opensearch.GetStatus().GetConditions(), "opensearch.aiven.io/ObservedState")
			Expect(updatedCondition).NotTo(BeNil())
			Expect(updatedCondition.Status).To(Equal(metav1.ConditionTrue))
			Expect(updatedCondition.Message).To(Equal("OpenSearch is in state: RUNNING"))
			// Transition time should be set (not zero) since status changed from False to True
			Expect(updatedCondition.LastTransitionTime.IsZero()).To(BeFalse())

			By("cleaning up")
			Expect(k8sClient.Delete(context.Background(), opensearch)).To(Succeed())
		})

		It("should not update transition time when state changes from REBALANCING to RUNNING", func() {
			opensearchName := "state-no-transition-test"
			opensearchKey := types.NamespacedName{Name: opensearchName, Namespace: opensearchStateTestNamespace}
			aivenOpenSearchName := "opensearch-" + opensearchStateTestNamespace + "-" + opensearchName

			By("creating an OpenSearch resource")
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      opensearchName,
					Namespace: opensearchStateTestNamespace,
				},
				Spec: v1.OpenSearchSpec{
					Tier:      v1.OpenSearchTierSingleNode,
					Memory:    v1.OpenSearchMemory4GB,
					Version:   v1.OpenSearchVersionV2,
					StorageGB: 80,
				},
			}
			Expect(k8sClient.Create(context.Background(), opensearch)).To(Succeed())

			By("reconciling the OpenSearch resource (initial)")
			_, err := controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: opensearchKey})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Aiven OpenSearch was created")
			aivenOpenSearch := &aiven_v1alpha1.OpenSearch{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenOpenSearchName, Namespace: opensearchStateTestNamespace}, aivenOpenSearch)).To(Succeed())

			By("simulating Aiven OpenSearch state set to REBALANCING")
			aivenOpenSearch.Status.State = "REBALANCING"
			Expect(k8sClient.Status().Update(context.Background(), aivenOpenSearch)).To(Succeed())

			By("reconciling to pick up REBALANCING state")
			_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: opensearchKey})
			Expect(err).NotTo(HaveOccurred())

			By("capturing the transition time after REBALANCING")
			Expect(k8sClient.Get(context.Background(), opensearchKey, opensearch)).To(Succeed())
			rebalancingCondition := meta.FindStatusCondition(opensearch.GetStatus().GetConditions(), "opensearch.aiven.io/ObservedState")
			Expect(rebalancingCondition).NotTo(BeNil())
			Expect(rebalancingCondition.Status).To(Equal(metav1.ConditionTrue))
			Expect(rebalancingCondition.Message).To(Equal("OpenSearch is in state: REBALANCING"))
			rebalancingTransitionTime := rebalancingCondition.LastTransitionTime

			By("simulating Aiven OpenSearch state change to RUNNING")
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenOpenSearchName, Namespace: opensearchStateTestNamespace}, aivenOpenSearch)).To(Succeed())
			aivenOpenSearch.Status.State = stateRunning
			Expect(k8sClient.Status().Update(context.Background(), aivenOpenSearch)).To(Succeed())

			By("reconciling the OpenSearch resource again")
			_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: opensearchKey})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the condition message changed but transition time stayed the same")
			Expect(k8sClient.Get(context.Background(), opensearchKey, opensearch)).To(Succeed())
			runningCondition := meta.FindStatusCondition(opensearch.GetStatus().GetConditions(), "opensearch.aiven.io/ObservedState")
			Expect(runningCondition).NotTo(BeNil())
			Expect(runningCondition.Status).To(Equal(metav1.ConditionTrue))
			Expect(runningCondition.Message).To(Equal("OpenSearch is in state: RUNNING"))
			// Transition time should NOT be updated since status remained True
			Expect(runningCondition.LastTransitionTime.Time).To(Equal(rebalancingTransitionTime.Time))

			By("cleaning up")
			Expect(k8sClient.Delete(context.Background(), opensearch)).To(Succeed())
		})
	})

	Describe("MinimalAivenOpenSearch", func() {
		It("should create minimal Aiven OpenSearch with correct metadata using namespaced name", func() {
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-opensearch",
					Namespace: "test-ns",
					Labels: map[string]string{
						"team": "test-team",
					},
				},
			}

			result := resourcecreator.MinimalAivenOpenSearch(opensearch)

			Expect(result.Name).To(Equal("opensearch-test-ns-test-opensearch"))
			Expect(result.Namespace).To(Equal("test-ns"))
			Expect(result.Kind).To(Equal("OpenSearch"))
			Expect(result.APIVersion).To(Equal("aiven.io/v1alpha1"))
		})
	})

	Describe("MinimalOpenSearchServiceIntegration", func() {
		It("should create minimal ServiceIntegration with correct metadata", func() {
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-opensearch",
					Namespace: "test-ns",
				},
			}

			result := resourcecreator.MinimalOpenSearchServiceIntegration(opensearch)

			Expect(result.Name).To(Equal("opensearch-test-ns-test-opensearch"))
			Expect(result.Namespace).To(Equal("test-ns"))
			Expect(result.Kind).To(Equal("ServiceIntegration"))
			Expect(result.APIVersion).To(Equal("aiven.io/v1alpha1"))
		})
	})

	Describe("Memory and Tier plan mapping", func() {
		tiers := []struct {
			tier         v1.OpenSearchTier
			memory       v1.OpenSearchMemory
			expectedPlan string
		}{
			{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory2GB, "hobbyist"},
			{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory4GB, "startup-4"},
			{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory8GB, "startup-8"},
			{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory16GB, "startup-16"},
			{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory32GB, "startup-32"},
			{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory64GB, "startup-64"},
			{v1.OpenSearchTierHighAvailability, v1.OpenSearchMemory4GB, "business-4"},
			{v1.OpenSearchTierHighAvailability, v1.OpenSearchMemory8GB, "business-8"},
			{v1.OpenSearchTierHighAvailability, v1.OpenSearchMemory16GB, "business-16"},
			{v1.OpenSearchTierHighAvailability, v1.OpenSearchMemory32GB, "business-32"},
			{v1.OpenSearchTierHighAvailability, v1.OpenSearchMemory64GB, "business-64"},
		}

		aiven := config.Aiven{
			Project: "test-project",
		}
		tenant := config.Tenant{
			Name: "test-tenant",
		}

		for _, tc := range tiers {
			It("should map "+string(tc.tier)+"/"+string(tc.memory)+" to "+tc.expectedPlan, func() {
				// Use appropriate storage for the plan
				storage := 80
				if tc.memory == v1.OpenSearchMemory2GB {
					storage = 16
				} else if tc.tier == v1.OpenSearchTierHighAvailability {
					switch tc.memory {
					case v1.OpenSearchMemory4GB:
						storage = 240
					case v1.OpenSearchMemory8GB:
						storage = 525
					case v1.OpenSearchMemory16GB:
						storage = 1050
					case v1.OpenSearchMemory32GB:
						storage = 2100
					case v1.OpenSearchMemory64GB:
						storage = 4200
					}
				} else {
					switch tc.memory {
					case v1.OpenSearchMemory8GB:
						storage = 175
					case v1.OpenSearchMemory16GB:
						storage = 350
					case v1.OpenSearchMemory32GB:
						storage = 700
					case v1.OpenSearchMemory64GB:
						storage = 1400
					}
				}

				opensearch := &v1.OpenSearch{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-opensearch",
						Namespace: "test-ns",
					},
					Spec: v1.OpenSearchSpec{
						Tier:      tc.tier,
						Memory:    tc.memory,
						Version:   v1.OpenSearchVersionV2,
						StorageGB: storage,
					},
				}

				result, err := resourcecreator.CreateAivenOpenSearchSpec(scheme.Scheme, opensearch, aiven, tenant)
				Expect(err).NotTo(HaveOccurred())

				Expect(result.Spec.Plan).To(Equal(tc.expectedPlan))
			})
		}
	})

	When("reconciling an OpenSearch resource", Serial, Ordered, func() {
		const (
			testNamespace = "os-sync-integration-ns"
		)

		var reconciler *synchronizer.Synchronizer[*v1.OpenSearch, OpenSearchPreparedData]

		BeforeAll(func() {
			By("using a fresh recorder to avoid blocking on full channel from previous tests")
			recorder := events.NewRecorder(record.NewFakeRecorder(100))

			By("creating a synchronizer for opensearch")
			opensearchReconciler := &OpenSearchReconciler{
				Aiven: config.Aiven{
					Project:                      "test-project",
					ProjectVPCID:                 "test-vpc-id",
					MetricsDestinationEndpointID: "test-metrics-service",
				},
				Tenant:   config.Tenant{Name: "test-tenant"},
				Recorder: recorder,
				Scheme:   scheme.Scheme,
			}
			reconciler = synchronizer.NewSynchronizer(k8sClient, scheme.Scheme, opensearchReconciler, recorder)

			By("creating the resource namespace")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		})

		When("the resource is created", func() {
			It("should set .Status.ReconcilePhase to Completed after first reconcile (finalizer addition)", func() {
				testOpenSearchName := "sync-first-reconcile"

				By("creating the resource")
				opensearch := &v1.OpenSearch{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testOpenSearchName,
						Namespace: testNamespace,
					},
					Spec: v1.OpenSearchSpec{
						Tier:      v1.OpenSearchTierSingleNode,
						Memory:    v1.OpenSearchMemory4GB,
						Version:   v1.OpenSearchVersionV2,
						StorageGB: 80,
					},
				}
				Expect(k8sClient.Create(ctx, opensearch)).To(Succeed())

				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      opensearch.Name,
						Namespace: opensearch.Namespace,
					},
				}

				By("reconciling the created resource (first reconcile adds finalizer)")
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				By("verifying that .Status.ReconcilePhase is Completed")
				updatedOpenSearch := &v1.OpenSearch{}
				Expect(k8sClient.Get(ctx, req.NamespacedName, updatedOpenSearch)).To(Succeed())
				Expect(updatedOpenSearch.Status).NotTo(BeNil())
				Expect(updatedOpenSearch.Status.ReconcilePhase).To(Equal("Completed"))

				By("verifying that the finalizer was added")
				Expect(updatedOpenSearch.GetFinalizers()).To(ContainElement("opensearch.nais.io/finalizer"))

				By("verifying that ObservedGeneration matches the resource generation")
				Expect(updatedOpenSearch.Status.ObservedGeneration).To(Equal(updatedOpenSearch.Generation))
			})

			It("should reach Completed after spec update triggers reconciliation", func() {
				testOpenSearchName := "sync-spec-update"

				By("creating the resource")
				opensearch := &v1.OpenSearch{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testOpenSearchName,
						Namespace: testNamespace,
					},
					Spec: v1.OpenSearchSpec{
						Tier:      v1.OpenSearchTierSingleNode,
						Memory:    v1.OpenSearchMemory4GB,
						Version:   v1.OpenSearchVersionV2,
						StorageGB: 80,
					},
				}
				Expect(k8sClient.Create(ctx, opensearch)).To(Succeed())

				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      opensearch.Name,
						Namespace: opensearch.Namespace,
					},
				}

				By("reconciling the created resource (first reconcile adds finalizer)")
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				By("verifying first reconcile completed successfully")
				firstOpenSearch := &v1.OpenSearch{}
				Expect(k8sClient.Get(ctx, req.NamespacedName, firstOpenSearch)).To(Succeed())
				Expect(firstOpenSearch.Status.ReconcilePhase).To(Equal("Completed"))
				initialGeneration := firstOpenSearch.Generation

				By("updating the spec to trigger a new reconciliation")
				firstOpenSearch.Spec.StorageGB = 90
				Expect(k8sClient.Update(ctx, firstOpenSearch)).To(Succeed())

				By("verifying the generation increased")
				Expect(k8sClient.Get(ctx, req.NamespacedName, firstOpenSearch)).To(Succeed())
				Expect(firstOpenSearch.Generation).To(BeNumerically(">", initialGeneration))

				By("reconciling after spec change (no finalizer change this time)")
				_, err = reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				By("verifying that .Status.ReconcilePhase is Completed")
				updatedOpenSearch := &v1.OpenSearch{}
				Expect(k8sClient.Get(ctx, req.NamespacedName, updatedOpenSearch)).To(Succeed())
				Expect(updatedOpenSearch.Status).NotTo(BeNil())
				Expect(updatedOpenSearch.Status.ReconcilePhase).To(Equal("Completed"))

				By("verifying that ObservedGeneration matches the new generation")
				Expect(updatedOpenSearch.Status.ObservedGeneration).To(Equal(updatedOpenSearch.Generation))
			})
		})
	})
})
