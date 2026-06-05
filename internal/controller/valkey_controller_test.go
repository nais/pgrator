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
	kevents "k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/nais/pgrator/internal/config"
	valkeyrc "github.com/nais/pgrator/internal/resourcecreator/valkey"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	aiven_v1alpha1 "github.com/nais/pgrator/internal/thirdparty/aiven/v1alpha1"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
)

var (
	_ reconciler.Reconciler[*v1.Valkey, ValkeyPreparedData] = &ValkeyReconciler{}
	_ reconciler.FinalizerNamer                             = &ValkeyReconciler{}
)

var _ = Describe("Valkey Controller", func() {
	Describe("CreateAivenValkeySpec", func() {
		const (
			testValkeyName = "my-valkey"
			testTeamName   = "my-team"
		)
		aiven := config.Aiven{
			Project:      "test-project",
			ProjectVPCID: "vpc-123",
		}
		tenant := config.Tenant{
			Name: "test-tenant",
		}

		It("should create basic valkey with correct fields", func() {
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testValkeyName,
					Namespace: testTeamName,
					Labels: map[string]string{
						"team": testTeamName,
					},
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}

			result, err := valkeyrc.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Spec.Project).To(Equal("test-project"))
			Expect(result.Spec.Plan).To(Equal("startup-4"))
			Expect(result.Spec.ProjectVPCID).To(Equal("vpc-123"))
			Expect(result.Spec.TerminationProtection).NotTo(BeNil())
			Expect(*result.Spec.TerminationProtection).To(BeTrue())
			Expect(result.Spec.UserConfig).To(BeNil())
			Expect(result.Spec.Tags["team"]).To(Equal(testTeamName))
			Expect(result.Spec.Tags["app"]).To(Equal(testValkeyName))
			Expect(result.Spec.Tags["tenant"]).To(Equal("test-tenant"))
			// Verify Aiven resource uses namespaced name
			Expect(result.Name).To(Equal("valkey-" + testTeamName + "-" + testValkeyName))
		})

		It("should set maxMemoryPolicy in user config", func() {
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "configured-valkey",
					Namespace: "config-team",
				},
				Spec: v1.ValkeySpec{
					Tier:            v1.ValkeyTierSingleNode,
					Memory:          v1.ValkeyMemory4GB,
					MaxMemoryPolicy: v1.ValkeyMaxMemoryPolicyVolatileLRU,
				},
			}

			result, err := valkeyrc.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Spec.UserConfig).NotTo(BeNil())
			Expect(result.Spec.UserConfig.ValkeyMaxmemoryPolicy).NotTo(BeNil())
			Expect(*result.Spec.UserConfig.ValkeyMaxmemoryPolicy).To(Equal("volatile-lru"))
		})

		It("should set notifyKeyspaceEvents in user config", func() {
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "events-valkey",
					Namespace: "events-team",
				},
				Spec: v1.ValkeySpec{
					Tier:                 v1.ValkeyTierSingleNode,
					Memory:               v1.ValkeyMemory4GB,
					NotifyKeyspaceEvents: "KEA",
				},
			}

			result, err := valkeyrc.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Spec.UserConfig).NotTo(BeNil())
			Expect(result.Spec.UserConfig.ValkeyNotifyKeyspaceEvents).NotTo(BeNil())
			Expect(*result.Spec.UserConfig.ValkeyNotifyKeyspaceEvents).To(Equal("KEA"))
		})

		It("should set all user config options", func() {
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "full-config-valkey",
					Namespace: "full-team",
				},
				Spec: v1.ValkeySpec{
					Tier:                 v1.ValkeyTierHighAvailability,
					Memory:               v1.ValkeyMemory14GB,
					MaxMemoryPolicy:      v1.ValkeyMaxMemoryPolicyAllkeysLFU,
					NotifyKeyspaceEvents: "Ex",
					Persistence: &v1.ValkeyPersistence{
						Disabled: true,
					},
					Databases: new(32),
				},
			}

			result, err := valkeyrc.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Spec.Plan).To(Equal("business-14"))
			Expect(result.Spec.UserConfig).NotTo(BeNil())
			Expect(*result.Spec.UserConfig.ValkeyMaxmemoryPolicy).To(Equal("allkeys-lfu"))
			Expect(*result.Spec.UserConfig.ValkeyNotifyKeyspaceEvents).To(Equal("Ex"))
			Expect(*result.Spec.UserConfig.ValkeyPersistence).To(Equal("off"))
			Expect(*result.Spec.UserConfig.ValkeyNumberOfDatabases).To(Equal(32))
		})

		It("should preserve labels from source valkey", func() {
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "labeled-valkey",
					Namespace: "labeled-team",
					Labels: map[string]string{
						"team":         "labeled-team",
						"custom-label": "custom-value",
						"another":      "label",
					},
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}

			result, err := valkeyrc.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Labels["team"]).To(Equal("labeled-team"))
			Expect(result.Labels["custom-label"]).To(Equal("custom-value"))
			Expect(result.Labels["another"]).To(Equal("label"))
			Expect(result.Labels["valkey.nais.io/name"]).To(Equal("labeled-valkey"))
			Expect(result.Name).To(Equal("valkey-labeled-team-labeled-valkey"))
		})
	})

	Describe("CreateServiceIntegrationSpec", func() {
		It("should create service integration with correct fields", func() {
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-valkey",
					Namespace: "my-team",
					Labels: map[string]string{
						"team": "my-team",
					},
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}
			cfg := config.Aiven{
				Project:                      "test-project",
				MetricsDestinationEndpointID: "metrics-service",
			}

			result, err := valkeyrc.CreateServiceIntegrationSpec(scheme.Scheme, valkey, cfg)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Name).To(Equal("valkey-my-team-my-valkey"))
			Expect(result.Namespace).To(Equal("my-team"))
			Expect(result.Spec.Project).To(Equal("test-project"))
			Expect(result.Spec.IntegrationType).To(Equal("prometheus"))
			Expect(result.Spec.SourceServiceName).To(Equal("valkey-my-team-my-valkey"))
			Expect(result.Spec.DestinationEndpointID).To(Equal("metrics-service"))
		})
	})

	Describe("aivenValkeyConditionGetter", func() {
		It("should return ObservedState=True when state is non-empty", func() {
			aivenValkey := &aiven_v1alpha1.Valkey{
				Status: aiven_v1alpha1.ServiceStatus{
					State: "RUNNING",
				},
			}
			aivenValkey.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("Valkey"))

			conditions := aivenValkeyConditionGetter(aivenValkey, scheme.Scheme)

			Expect(conditions).To(HaveLen(1))
			observedState := meta.FindStatusCondition(conditions, "valkey.aiven.io/ObservedState")
			Expect(observedState).NotTo(BeNil())
			Expect(observedState.Status).To(Equal(metav1.ConditionTrue))
			Expect(observedState.Reason).To(Equal("Reconciled"))
			Expect(observedState.Message).To(Equal("Valkey is in state: RUNNING"))
		})

		It("should return ObservedState=False when state is empty", func() {
			aivenValkey := &aiven_v1alpha1.Valkey{
				Status: aiven_v1alpha1.ServiceStatus{
					State: "",
				},
			}
			aivenValkey.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("Valkey"))

			conditions := aivenValkeyConditionGetter(aivenValkey, scheme.Scheme)

			Expect(conditions).To(HaveLen(1))
			observedState := meta.FindStatusCondition(conditions, "valkey.aiven.io/ObservedState")
			Expect(observedState).NotTo(BeNil())
			Expect(observedState.Status).To(Equal(metav1.ConditionFalse))
			Expect(observedState.Reason).To(Equal("Reconciled"))
			Expect(observedState.Message).To(Equal("Valkey is in state: "))
		})
	})

	Describe("serviceIntegrationConditionGetter", func() {
		It("should return nil", func() {
			integration := &aiven_v1alpha1.ServiceIntegration{
				Status: aiven_v1alpha1.ServiceIntegrationStatus{
					Conditions: []metav1.Condition{
						{
							Type:    "Running",
							Status:  metav1.ConditionTrue,
							Reason:  "CheckRunning",
							Message: "Integration is running",
						},
					},
				},
			}
			integration.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("ServiceIntegration"))

			conditions := serviceIntegrationConditionGetter(integration, scheme.Scheme)

			Expect(conditions).To(BeNil())
		})
	})

	Describe("Valkey state change reconciliation", func() {
		const (
			valkeyStateTestNamespace = "valkey-state-test"
		)

		var controllerReconciler *synchronizer.Synchronizer[*v1.Valkey, ValkeyPreparedData]

		BeforeEach(func() {
			By("using a fresh recorder to avoid blocking on full channel from previous tests")
			recorder := events.NewRecorder(kevents.NewFakeRecorder(100))

			By("creating the synchronizer for valkey")
			valkeyReconciler := &ValkeyReconciler{
				Aiven: config.Aiven{
					Project:                      "test-project",
					ProjectVPCID:                 "test-vpc-id",
					MetricsDestinationEndpointID: "test-metrics-service",
				},
				Tenant:   config.Tenant{Name: "test-tenant"},
				Recorder: recorder,
				Scheme:   k8sClient.Scheme(),
			}
			controllerReconciler = synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), valkeyReconciler, recorder)

			By("creating the test namespace")
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: valkeyStateTestNamespace,
				},
			}
			err := k8sClient.Get(context.Background(), types.NamespacedName{Name: valkeyStateTestNamespace}, ns)
			if apierrors.IsNotFound(err) {
				Expect(k8sClient.Create(context.Background(), ns)).To(Succeed())
			}
		})

		It("should update condition when Aiven Valkey state changes from empty to RUNNING", func() {
			valkeyName := "state-change-test"
			valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: valkeyStateTestNamespace}
			aivenValkeyName := "valkey-" + valkeyStateTestNamespace + "-" + valkeyName

			By("creating a Valkey resource")
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      valkeyName,
					Namespace: valkeyStateTestNamespace,
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}
			Expect(k8sClient.Create(context.Background(), valkey)).To(Succeed())

			By("reconciling the Valkey resource (initial)")
			_, err := controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Aiven Valkey was created")
			aivenValkey := &aiven_v1alpha1.Valkey{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenValkeyName, Namespace: valkeyStateTestNamespace}, aivenValkey)).To(Succeed())

			By("checking initial condition (state is empty)")
			Expect(k8sClient.Get(context.Background(), valkeyKey, valkey)).To(Succeed())
			initialCondition := meta.FindStatusCondition(valkey.GetStatus().GetConditions(), "valkey.aiven.io/ObservedState")
			Expect(initialCondition).NotTo(BeNil())
			Expect(initialCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(initialCondition.Message).To(Equal("Valkey is in state: "))
			Expect(initialCondition.LastTransitionTime.IsZero()).To(BeFalse())

			By("simulating Aiven Valkey state change to RUNNING")
			aivenValkey.Status.State = "RUNNING"
			Expect(k8sClient.Status().Update(context.Background(), aivenValkey)).To(Succeed())

			By("reconciling the Valkey resource again")
			_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the condition was updated with new state")
			Expect(k8sClient.Get(context.Background(), valkeyKey, valkey)).To(Succeed())
			updatedCondition := meta.FindStatusCondition(valkey.GetStatus().GetConditions(), "valkey.aiven.io/ObservedState")
			Expect(updatedCondition).NotTo(BeNil())
			Expect(updatedCondition.Status).To(Equal(metav1.ConditionTrue))
			Expect(updatedCondition.Message).To(Equal("Valkey is in state: RUNNING"))
			// Transition time should be set (not zero) since status changed from False to True
			Expect(updatedCondition.LastTransitionTime.IsZero()).To(BeFalse())

			By("cleaning up")
			Expect(k8sClient.Delete(context.Background(), valkey)).To(Succeed())
		})

		It("should not update transition time when state changes from REBALANCING to RUNNING", func() {
			valkeyName := "state-no-transition-test"
			valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: valkeyStateTestNamespace}
			aivenValkeyName := "valkey-" + valkeyStateTestNamespace + "-" + valkeyName

			By("creating a Valkey resource")
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      valkeyName,
					Namespace: valkeyStateTestNamespace,
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}
			Expect(k8sClient.Create(context.Background(), valkey)).To(Succeed())

			By("reconciling the Valkey resource (initial)")
			_, err := controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Aiven Valkey was created")
			aivenValkey := &aiven_v1alpha1.Valkey{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenValkeyName, Namespace: valkeyStateTestNamespace}, aivenValkey)).To(Succeed())

			By("simulating Aiven Valkey state set to REBALANCING")
			aivenValkey.Status.State = "REBALANCING"
			Expect(k8sClient.Status().Update(context.Background(), aivenValkey)).To(Succeed())

			By("reconciling to pick up REBALANCING state")
			_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())

			By("capturing the transition time after REBALANCING")
			Expect(k8sClient.Get(context.Background(), valkeyKey, valkey)).To(Succeed())
			rebalancingCondition := meta.FindStatusCondition(valkey.GetStatus().GetConditions(), "valkey.aiven.io/ObservedState")
			Expect(rebalancingCondition).NotTo(BeNil())
			Expect(rebalancingCondition.Status).To(Equal(metav1.ConditionTrue))
			Expect(rebalancingCondition.Message).To(Equal("Valkey is in state: REBALANCING"))
			rebalancingTransitionTime := rebalancingCondition.LastTransitionTime

			By("simulating Aiven Valkey state change to RUNNING")
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenValkeyName, Namespace: valkeyStateTestNamespace}, aivenValkey)).To(Succeed())
			aivenValkey.Status.State = "RUNNING"
			Expect(k8sClient.Status().Update(context.Background(), aivenValkey)).To(Succeed())

			By("reconciling the Valkey resource again")
			_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the condition message changed but transition time stayed the same")
			Expect(k8sClient.Get(context.Background(), valkeyKey, valkey)).To(Succeed())
			runningCondition := meta.FindStatusCondition(valkey.GetStatus().GetConditions(), "valkey.aiven.io/ObservedState")
			Expect(runningCondition).NotTo(BeNil())
			Expect(runningCondition.Status).To(Equal(metav1.ConditionTrue))
			Expect(runningCondition.Message).To(Equal("Valkey is in state: RUNNING"))
			// Transition time should NOT be updated since status remained True
			Expect(runningCondition.LastTransitionTime.Time).To(Equal(rebalancingTransitionTime.Time))

			By("cleaning up")
			Expect(k8sClient.Delete(context.Background(), valkey)).To(Succeed())
		})
	})

	Describe("MinimalAivenValkey", func() {
		It("should create minimal Aiven Valkey with correct metadata using namespaced name", func() {
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-valkey",
					Namespace: "test-ns",
					Labels: map[string]string{
						"team": "test-team",
					},
				},
			}

			result := valkeyrc.Minimal(valkey)

			Expect(result.Name).To(Equal("valkey-test-ns-test-valkey"))
			Expect(result.Namespace).To(Equal("test-ns"))
			Expect(result.Kind).To(Equal("Valkey"))
			Expect(result.APIVersion).To(Equal("aiven.io/v1alpha1"))
		})
	})

	Describe("MinimalServiceIntegration", func() {
		It("should create minimal ServiceIntegration with correct metadata", func() {
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-valkey",
					Namespace: "test-ns",
				},
			}

			result := valkeyrc.MinimalServiceIntegration(valkey)

			Expect(result.Name).To(Equal("valkey-test-ns-test-valkey"))
			Expect(result.Namespace).To(Equal("test-ns"))
			Expect(result.Kind).To(Equal("ServiceIntegration"))
			Expect(result.APIVersion).To(Equal("aiven.io/v1alpha1"))
		})
	})

	Describe("MaxMemoryPolicy handling", func() {
		policies := []v1.ValkeyMaxMemoryPolicy{
			v1.ValkeyMaxMemoryPolicyAllkeysLFU,
			v1.ValkeyMaxMemoryPolicyAllkeysLRU,
			v1.ValkeyMaxMemoryPolicyAllkeysRandom,
			v1.ValkeyMaxMemoryPolicyNoEviction,
			v1.ValkeyMaxMemoryPolicyVolatileLFU,
			v1.ValkeyMaxMemoryPolicyVolatileLRU,
			v1.ValkeyMaxMemoryPolicyVolatileRandom,
			v1.ValkeyMaxMemoryPolicyVolatileTTL,
		}

		aiven := config.Aiven{
			Project: "test-project",
		}
		tenant := config.Tenant{
			Name: "test-tenant",
		}

		for _, policy := range policies {
			It("should correctly handle "+string(policy)+" policy", func() {
				valkey := &v1.Valkey{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-valkey",
						Namespace: "test-ns",
					},
					Spec: v1.ValkeySpec{
						Tier:            v1.ValkeyTierSingleNode,
						Memory:          v1.ValkeyMemory4GB,
						MaxMemoryPolicy: policy,
					},
				}

				result, err := valkeyrc.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
				Expect(err).NotTo(HaveOccurred())

				Expect(result.Spec.UserConfig).NotTo(BeNil())
				Expect(result.Spec.UserConfig.ValkeyMaxmemoryPolicy).NotTo(BeNil())
				Expect(*result.Spec.UserConfig.ValkeyMaxmemoryPolicy).To(Equal(string(policy)))
			})
		}
	})

	When("reconciling a Valkey resource", Serial, Ordered, func() {
		const (
			testNamespace = "sync-integration-ns"
		)

		var reconciler *synchronizer.Synchronizer[*v1.Valkey, ValkeyPreparedData]

		BeforeAll(func() {
			By("using a fresh recorder to avoid blocking on full channel from previous tests")
			recorder := events.NewRecorder(kevents.NewFakeRecorder(100))

			By("creating a synchronizer for valkey")
			valkeyReconciler := &ValkeyReconciler{
				Aiven: config.Aiven{
					Project:                      "test-project",
					ProjectVPCID:                 "test-vpc-id",
					MetricsDestinationEndpointID: "test-metrics-service",
				},
				Tenant:   config.Tenant{Name: "test-tenant"},
				Recorder: recorder,
				Scheme:   scheme.Scheme,
			}
			reconciler = synchronizer.NewSynchronizer(k8sClient, scheme.Scheme, valkeyReconciler, recorder)

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
				testValkeyName := "sync-first-reconcile"

				By("creating the resource")
				valkey := &v1.Valkey{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testValkeyName,
						Namespace: testNamespace,
					},
					Spec: v1.ValkeySpec{
						Tier:   v1.ValkeyTierSingleNode,
						Memory: v1.ValkeyMemory4GB,
					},
				}
				Expect(k8sClient.Create(ctx, valkey)).To(Succeed())

				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      valkey.Name,
						Namespace: valkey.Namespace,
					},
				}

				By("reconciling the created resource (first reconcile adds finalizer)")
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				By("verifying that .Status.ReconcilePhase is Completed")
				updatedValkey := &v1.Valkey{}
				Expect(k8sClient.Get(ctx, req.NamespacedName, updatedValkey)).To(Succeed())
				Expect(updatedValkey.Status).NotTo(BeNil())
				Expect(updatedValkey.Status.ReconcilePhase).To(Equal("Completed"))

				By("verifying that the finalizer was added")
				Expect(updatedValkey.GetFinalizers()).To(ContainElement("valkey.nais.io/finalizer"))

				By("verifying that ObservedGeneration matches the resource generation")
				Expect(updatedValkey.Status.ObservedGeneration).To(Equal(updatedValkey.Generation))
			})

			It("should reach Completed after spec update triggers reconciliation", func() {
				testValkeyName := "sync-spec-update"

				By("creating the resource")
				valkey := &v1.Valkey{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testValkeyName,
						Namespace: testNamespace,
					},
					Spec: v1.ValkeySpec{
						Tier:   v1.ValkeyTierSingleNode,
						Memory: v1.ValkeyMemory4GB,
					},
				}
				Expect(k8sClient.Create(ctx, valkey)).To(Succeed())

				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      valkey.Name,
						Namespace: valkey.Namespace,
					},
				}

				By("reconciling the created resource (first reconcile adds finalizer)")
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				By("verifying first reconcile completed successfully")
				firstValkey := &v1.Valkey{}
				Expect(k8sClient.Get(ctx, req.NamespacedName, firstValkey)).To(Succeed())
				Expect(firstValkey.Status.ReconcilePhase).To(Equal("Completed"))
				initialGeneration := firstValkey.Generation

				By("updating the spec to trigger a new reconciliation")
				firstValkey.Spec.Memory = v1.ValkeyMemory8GB
				Expect(k8sClient.Update(ctx, firstValkey)).To(Succeed())

				By("verifying the generation increased")
				Expect(k8sClient.Get(ctx, req.NamespacedName, firstValkey)).To(Succeed())
				Expect(firstValkey.Generation).To(BeNumerically(">", initialGeneration))

				By("reconciling after spec change (no finalizer change this time)")
				_, err = reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				By("verifying that .Status.ReconcilePhase is Completed")
				updatedValkey := &v1.Valkey{}
				Expect(k8sClient.Get(ctx, req.NamespacedName, updatedValkey)).To(Succeed())
				Expect(updatedValkey.Status).NotTo(BeNil())
				Expect(updatedValkey.Status.ReconcilePhase).To(Equal("Completed"))

				By("verifying that ObservedGeneration matches the new generation")
				Expect(updatedValkey.Status.ObservedGeneration).To(Equal(updatedValkey.Generation))
			})
		})
	})

	When("deleting a Valkey resource", Serial, Ordered, func() {
		const (
			deleteTestNamespace = "valkey-delete-test"
		)

		var syncReconciler *synchronizer.Synchronizer[*v1.Valkey, ValkeyPreparedData]

		BeforeAll(func() {
			recorder := events.NewRecorder(kevents.NewFakeRecorder(100))
			valkeyReconciler := &ValkeyReconciler{
				Aiven: config.Aiven{
					Project:                      "test-project",
					ProjectVPCID:                 "test-vpc-id",
					MetricsDestinationEndpointID: "test-metrics-service",
				},
				Tenant:   config.Tenant{Name: "test-tenant"},
				Recorder: recorder,
				Scheme:   scheme.Scheme,
			}
			syncReconciler = synchronizer.NewSynchronizer(k8sClient, scheme.Scheme, valkeyReconciler, recorder)

			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: deleteTestNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		})

		It("should refuse deletion without allowDeletion annotation", func() {
			valkeyName := "delete-no-annotation"
			valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: deleteTestNamespace}

			By("creating and reconciling a Valkey resource")
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      valkeyName,
					Namespace: deleteTestNamespace,
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}
			Expect(k8sClient.Create(ctx, valkey)).To(Succeed())

			_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())

			By("deleting the Valkey resource without the annotation")
			Expect(k8sClient.Get(ctx, valkeyKey, valkey)).To(Succeed())
			Expect(k8sClient.Delete(ctx, valkey)).To(Succeed())

			By("reconciling - should refuse deletion")
			_, err = syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("refusing to delete"))

			By("verifying the finalizer is still present (resource not deleted)")
			Expect(k8sClient.Get(ctx, valkeyKey, valkey)).To(Succeed())
			Expect(valkey.GetFinalizers()).To(ContainElement("valkey.nais.io/finalizer"))
		})

		It("should disable terminationProtection before deleting child resources", func() {
			valkeyName := "delete-with-protection"
			valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: deleteTestNamespace}
			aivenValkeyName := "valkey-" + deleteTestNamespace + "-" + valkeyName

			By("creating and reconciling a Valkey resource")
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      valkeyName,
					Namespace: deleteTestNamespace,
					Annotations: map[string]string{
						api.AllowDeletionAnnotation: "true",
					},
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}
			Expect(k8sClient.Create(ctx, valkey)).To(Succeed())

			_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Aiven Valkey was created with terminationProtection=true")
			aivenValkey := &aiven_v1alpha1.Valkey{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey)).To(Succeed())
			Expect(aivenValkey.Spec.TerminationProtection).NotTo(BeNil())
			Expect(*aivenValkey.Spec.TerminationProtection).To(BeTrue())

			By("deleting the Valkey resource")
			Expect(k8sClient.Get(ctx, valkeyKey, valkey)).To(Succeed())
			Expect(k8sClient.Delete(ctx, valkey)).To(Succeed())

			By("reconciling - should disable terminationProtection and requeue")
			result, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			By("verifying terminationProtection was disabled on the Aiven resource")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey)).To(Succeed())
			Expect(*aivenValkey.Spec.TerminationProtection).To(BeFalse())

			By("verifying the finalizer is still present")
			Expect(k8sClient.Get(ctx, valkeyKey, valkey)).To(Succeed())
			Expect(valkey.GetFinalizers()).To(ContainElement("valkey.nais.io/finalizer"))

			By("reconciling again - should now delete child resources and remove finalizer")
			result, err = syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("verifying the Aiven Valkey resource was deleted")
			err = k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			By("verifying the Valkey CR was garbage collected (finalizer removed)")
			err = k8sClient.Get(ctx, valkeyKey, valkey)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should skip terminationProtection dance when Aiven resource doesn't exist", func() {
			valkeyName := "delete-no-aiven"
			valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: deleteTestNamespace}

			By("creating a Valkey resource with the delete annotation")
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      valkeyName,
					Namespace: deleteTestNamespace,
					Annotations: map[string]string{
						api.AllowDeletionAnnotation: "true",
					},
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}
			Expect(k8sClient.Create(ctx, valkey)).To(Succeed())

			By("reconciling to add finalizer and create Aiven resource")
			_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())

			By("manually deleting the Aiven resource to simulate external deletion")
			aivenValkeyName := "valkey-" + deleteTestNamespace + "-" + valkeyName
			aivenValkey := &aiven_v1alpha1.Valkey{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey)).To(Succeed())
			Expect(k8sClient.Delete(ctx, aivenValkey)).To(Succeed())

			By("deleting the Valkey CR")
			Expect(k8sClient.Get(ctx, valkeyKey, valkey)).To(Succeed())
			Expect(k8sClient.Delete(ctx, valkey)).To(Succeed())

			By("reconciling - should complete immediately since Aiven resource is gone")
			result, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("verifying the Valkey CR was garbage collected")
			err = k8sClient.Get(ctx, valkeyKey, valkey)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should delete when terminationProtection is already disabled", func() {
			valkeyName := "delete-no-protection"
			valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: deleteTestNamespace}
			aivenValkeyName := "valkey-" + deleteTestNamespace + "-" + valkeyName

			By("creating and reconciling a Valkey resource")
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      valkeyName,
					Namespace: deleteTestNamespace,
					Annotations: map[string]string{
						api.AllowDeletionAnnotation: "true",
					},
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}
			Expect(k8sClient.Create(ctx, valkey)).To(Succeed())

			_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())

			By("manually disabling terminationProtection")
			aivenValkey := &aiven_v1alpha1.Valkey{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey)).To(Succeed())
			aivenValkey.Spec.TerminationProtection = ptr.To(false)
			Expect(k8sClient.Update(ctx, aivenValkey)).To(Succeed())

			By("deleting the Valkey CR")
			Expect(k8sClient.Get(ctx, valkeyKey, valkey)).To(Succeed())
			Expect(k8sClient.Delete(ctx, valkey)).To(Succeed())

			By("reconciling - should delete immediately (no requeue)")
			result, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			By("verifying the Aiven resource was deleted")
			err = k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			By("verifying the Valkey CR was garbage collected")
			err = k8sClient.Get(ctx, valkeyKey, valkey)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})
})
