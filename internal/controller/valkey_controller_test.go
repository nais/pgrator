package controller

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/controller/resourcecreator"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/events"
	aiven_v1alpha1 "github.com/nais/pgrator/pkg/api/thirdparty/aiven/v1alpha1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
)

var _ = Describe("Valkey Controller", func() {
	Describe("ValidatingAdmissionPolicy", func() {
		It("should reject valkey with name too long for generated service name", func() {
			// The generated Valkey service name is "valkey-{namespace}-{name}"
			// which must be <= 63 characters. With "valkey-" (7 chars) and "-" (1 char),
			// name + namespace must be <= 55 characters.
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-admission-ns",
				},
			}
			err := k8sClient.Create(ctx, namespace)
			Expect(err).NotTo(HaveOccurred())

			// Create a name that's too long: namespace (17) + name (40) = 57 > 55
			longName := strings.Repeat("a", 40)
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      longName,
					Namespace: namespace.Name,
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}

			err = k8sClient.Create(ctx, valkey)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("metadata.name is too long"))
		})

		It("should accept valkey with name that fits within service name limit", func() {
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-admission-ok",
				},
			}
			err := k8sClient.Create(ctx, namespace)
			Expect(err).NotTo(HaveOccurred())

			// Create a name that fits: namespace (17) + name (10) = 27 <= 55
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "short-name",
					Namespace: namespace.Name,
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}

			err = k8sClient.Create(ctx, valkey)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ValkeyReconciler", func() {
		Describe("Name", func() {
			It("should return valkey.nais.io", func() {
				r := &ValkeyReconciler{}
				Expect(r.Name()).To(Equal("valkey.nais.io"))
			})
		})

		Describe("New", func() {
			It("should return a new Valkey instance", func() {
				r := &ValkeyReconciler{}
				got := r.New()
				Expect(got).NotTo(BeNil())
				_, ok := interface{}(got).(*v1.Valkey)
				Expect(ok).To(BeTrue())
			})
		})

		Describe("OwnedTypes", func() {
			It("should return Aiven Valkey and ServiceIntegration types", func() {
				r := &ValkeyReconciler{}
				got := r.OwnedTypes()
				Expect(got).To(HaveLen(2))

				_, ok := got[0].(*aiven_v1alpha1.Valkey)
				Expect(ok).To(BeTrue())

				_, ok = got[1].(*aiven_v1alpha1.ServiceIntegration)
				Expect(ok).To(BeTrue())
			})
		})

		Describe("AdditionalTypes", func() {
			It("should return nil", func() {
				r := &ValkeyReconciler{}
				Expect(r.AdditionalTypes()).To(BeNil())
			})
		})
	})

	Describe("CreateAivenValkeySpec", func() {
		const (
			testValkeyName = "my-valkey"
			testTeamName   = "my-team"
		)
		aiven := &config.Aiven{
			Project:      "test-project",
			ProjectVPCID: "vpc-123",
		}
		tenant := &config.Tenant{
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

			result, err := resourcecreator.CreateAivenValkeySpec(scheme.Scheme, valkey, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Spec.Project).To(Equal("test-project"))
			Expect(result.Spec.Plan).To(Equal("startup-4"))
			Expect(result.Spec.ProjectVPCID).To(Equal("vpc-123"))
			Expect(result.Spec.TerminationProtection).NotTo(BeNil())
			Expect(*result.Spec.TerminationProtection).To(BeFalse())
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

			result, err := resourcecreator.CreateAivenValkeySpec(scheme.Scheme, valkey, aiven, tenant)
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

			result, err := resourcecreator.CreateAivenValkeySpec(scheme.Scheme, valkey, aiven, tenant)
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
				},
			}

			result, err := resourcecreator.CreateAivenValkeySpec(scheme.Scheme, valkey, aiven, tenant)
			Expect(err).NotTo(HaveOccurred())

			Expect(result.Spec.Plan).To(Equal("business-14"))
			Expect(result.Spec.UserConfig).NotTo(BeNil())
			Expect(*result.Spec.UserConfig.ValkeyMaxmemoryPolicy).To(Equal("allkeys-lfu"))
			Expect(*result.Spec.UserConfig.ValkeyNotifyKeyspaceEvents).To(Equal("Ex"))
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

			result, err := resourcecreator.CreateAivenValkeySpec(scheme.Scheme, valkey, aiven, tenant)
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
			cfg := &config.Aiven{
				Project:                      "test-project",
				MetricsDestinationEndpointID: "metrics-service",
			}

			result, err := resourcecreator.CreateServiceIntegrationSpec(scheme.Scheme, valkey, cfg)
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
		It("should return Available=True for RUNNING state", func() {
			aivenValkey := &aiven_v1alpha1.Valkey{
				Status: aiven_v1alpha1.ServiceStatus{
					State: "RUNNING",
				},
			}
			aivenValkey.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("Valkey"))

			conditions := aivenValkeyConditionGetter(aivenValkey)

			Expect(conditions).To(HaveLen(3))
			available := findCondition(conditions, "valkey.aiven.io/Available")
			Expect(available).NotTo(BeNil())
			Expect(available.Status).To(Equal(metav1.ConditionTrue))
			Expect(available.Reason).To(Equal("RUNNING"))

			progressing := findCondition(conditions, "valkey.aiven.io/Progressing")
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))

			degraded := findCondition(conditions, "valkey.aiven.io/Degraded")
			Expect(degraded.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should return Progressing=True for REBUILDING state", func() {
			aivenValkey := &aiven_v1alpha1.Valkey{
				Status: aiven_v1alpha1.ServiceStatus{
					State: "REBUILDING",
				},
			}
			aivenValkey.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("Valkey"))

			conditions := aivenValkeyConditionGetter(aivenValkey)

			available := findCondition(conditions, "valkey.aiven.io/Available")
			Expect(available.Status).To(Equal(metav1.ConditionFalse))

			progressing := findCondition(conditions, "valkey.aiven.io/Progressing")
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))

			degraded := findCondition(conditions, "valkey.aiven.io/Degraded")
			Expect(degraded.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should return Progressing=True for REBALANCING state", func() {
			aivenValkey := &aiven_v1alpha1.Valkey{
				Status: aiven_v1alpha1.ServiceStatus{
					State: "REBALANCING",
				},
			}
			aivenValkey.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("Valkey"))

			conditions := aivenValkeyConditionGetter(aivenValkey)

			progressing := findCondition(conditions, "valkey.aiven.io/Progressing")
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should return Degraded=True for POWEROFF state", func() {
			aivenValkey := &aiven_v1alpha1.Valkey{
				Status: aiven_v1alpha1.ServiceStatus{
					State: "POWEROFF",
				},
			}
			aivenValkey.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("Valkey"))

			conditions := aivenValkeyConditionGetter(aivenValkey)

			available := findCondition(conditions, "valkey.aiven.io/Available")
			Expect(available.Status).To(Equal(metav1.ConditionFalse))

			progressing := findCondition(conditions, "valkey.aiven.io/Progressing")
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))

			degraded := findCondition(conditions, "valkey.aiven.io/Degraded")
			Expect(degraded.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should return Progressing=True for empty state", func() {
			aivenValkey := &aiven_v1alpha1.Valkey{
				Status: aiven_v1alpha1.ServiceStatus{
					State: "",
				},
			}
			aivenValkey.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("Valkey"))

			conditions := aivenValkeyConditionGetter(aivenValkey)

			available := findCondition(conditions, "valkey.aiven.io/Available")
			Expect(available.Status).To(Equal(metav1.ConditionFalse))
			Expect(available.Reason).To(Equal("Unknown"))

			progressing := findCondition(conditions, "valkey.aiven.io/Progressing")
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should use Aiven conditions when available", func() {
			aivenValkey := &aiven_v1alpha1.Valkey{
				Status: aiven_v1alpha1.ServiceStatus{
					State: "RUNNING",
					Conditions: []metav1.Condition{
						{
							Type:    "Ready",
							Status:  metav1.ConditionTrue,
							Reason:  "ServiceRunning",
							Message: "Service is fully operational",
						},
					},
				},
			}
			aivenValkey.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("Valkey"))

			conditions := aivenValkeyConditionGetter(aivenValkey)

			available := findCondition(conditions, "valkey.aiven.io/Available")
			Expect(available.Status).To(Equal(metav1.ConditionTrue))
			Expect(available.Reason).To(Equal("ServiceRunning"))
			Expect(available.Message).To(Equal("Service is fully operational"))
		})
	})

	Describe("serviceIntegrationConditionGetter", func() {
		It("should return Available=True for UpToDate condition", func() {
			integration := &aiven_v1alpha1.ServiceIntegration{
				Status: aiven_v1alpha1.ServiceIntegrationStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionTrue,
							Reason: "UpToDate",
						},
					},
				},
			}
			integration.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("ServiceIntegration"))

			conditions := serviceIntegrationConditionGetter(integration)

			Expect(conditions).To(HaveLen(3))
			available := findCondition(conditions, "serviceintegration.aiven.io/Available")
			Expect(available.Status).To(Equal(metav1.ConditionTrue))

			progressing := findCondition(conditions, "serviceintegration.aiven.io/Progressing")
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))

			degraded := findCondition(conditions, "serviceintegration.aiven.io/Degraded")
			Expect(degraded.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should return Available=True for Created condition", func() {
			integration := &aiven_v1alpha1.ServiceIntegration{
				Status: aiven_v1alpha1.ServiceIntegrationStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionTrue,
							Reason: "Created",
						},
					},
				},
			}
			integration.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("ServiceIntegration"))

			conditions := serviceIntegrationConditionGetter(integration)

			available := findCondition(conditions, "serviceintegration.aiven.io/Available")
			Expect(available.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should return Progressing=True for Creating condition", func() {
			integration := &aiven_v1alpha1.ServiceIntegration{
				Status: aiven_v1alpha1.ServiceIntegrationStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionFalse,
							Reason: "Creating",
						},
					},
				},
			}
			integration.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("ServiceIntegration"))

			conditions := serviceIntegrationConditionGetter(integration)

			available := findCondition(conditions, "serviceintegration.aiven.io/Available")
			Expect(available.Status).To(Equal(metav1.ConditionFalse))

			progressing := findCondition(conditions, "serviceintegration.aiven.io/Progressing")
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))

			degraded := findCondition(conditions, "serviceintegration.aiven.io/Degraded")
			Expect(degraded.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should return both Available and Progressing=True for Updating condition", func() {
			integration := &aiven_v1alpha1.ServiceIntegration{
				Status: aiven_v1alpha1.ServiceIntegrationStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "Ready",
							Status: metav1.ConditionTrue,
							Reason: "Updating",
						},
					},
				},
			}
			integration.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("ServiceIntegration"))

			conditions := serviceIntegrationConditionGetter(integration)

			available := findCondition(conditions, "serviceintegration.aiven.io/Available")
			Expect(available.Status).To(Equal(metav1.ConditionTrue))

			progressing := findCondition(conditions, "serviceintegration.aiven.io/Progressing")
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should return Degraded=True for Failed condition", func() {
			integration := &aiven_v1alpha1.ServiceIntegration{
				Status: aiven_v1alpha1.ServiceIntegrationStatus{
					Conditions: []metav1.Condition{
						{
							Type:    "Ready",
							Status:  metav1.ConditionFalse,
							Reason:  "CreateFailed",
							Message: "Failed to create",
						},
					},
				},
			}
			integration.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("ServiceIntegration"))

			conditions := serviceIntegrationConditionGetter(integration)

			available := findCondition(conditions, "serviceintegration.aiven.io/Available")
			Expect(available.Status).To(Equal(metav1.ConditionFalse))

			progressing := findCondition(conditions, "serviceintegration.aiven.io/Progressing")
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))

			degraded := findCondition(conditions, "serviceintegration.aiven.io/Degraded")
			Expect(degraded.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should handle empty conditions", func() {
			integration := &aiven_v1alpha1.ServiceIntegration{
				Status: aiven_v1alpha1.ServiceIntegrationStatus{
					Conditions: []metav1.Condition{},
				},
			}
			integration.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("ServiceIntegration"))

			conditions := serviceIntegrationConditionGetter(integration)

			Expect(conditions).To(HaveLen(3))
			available := findCondition(conditions, "serviceintegration.aiven.io/Available")
			Expect(available.Status).To(Equal(metav1.ConditionFalse))

			progressing := findCondition(conditions, "serviceintegration.aiven.io/Progressing")
			Expect(progressing.Status).To(Equal(metav1.ConditionFalse))

			degraded := findCondition(conditions, "serviceintegration.aiven.io/Degraded")
			Expect(degraded.Status).To(Equal(metav1.ConditionFalse))
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

			result := resourcecreator.MinimalAivenValkey(valkey)

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

			result := resourcecreator.MinimalServiceIntegration(valkey)

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

		aiven := &config.Aiven{
			Project: "test-project",
		}
		tenant := &config.Tenant{
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

				result, err := resourcecreator.CreateAivenValkeySpec(scheme.Scheme, valkey, aiven, tenant)
				Expect(err).NotTo(HaveOccurred())

				Expect(result.Spec.UserConfig).NotTo(BeNil())
				Expect(result.Spec.UserConfig.ValkeyMaxmemoryPolicy).NotTo(BeNil())
				Expect(*result.Spec.UserConfig.ValkeyMaxmemoryPolicy).To(Equal(string(policy)))
			})
		}
	})

	When("reconciling a Valkey resource", Serial, Ordered, func() {
		const (
			testNamespace  = "sync-integration-ns"
			testValkeyName = "sync-integration-valkey"
		)

		var reconciler *synchronizer.Synchronizer[*v1.Valkey, ValkeyPreparedData]

		BeforeAll(func() {
			By("using a fresh recorder to avoid blocking on full channel from previous tests")
			recorder := events.NewRecorder(record.NewFakeRecorder(100))

			By("creating a synchronizer for valkey")
			valkeyReconciler := &ValkeyReconciler{
				Aiven: &config.Aiven{
					Project:                      "test-project",
					ProjectVPCID:                 "test-vpc-id",
					MetricsDestinationEndpointID: "test-metrics-service",
				},
				Tenant:   &config.Tenant{Name: "test-tenant"},
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
			It("should set .Status.ReconcilePhase to Completed", func() {
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

				By("reconciling the created resource")
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				By("verifying that .Status.ReconcilePhase")
				updatedValkey := &v1.Valkey{}
				Expect(k8sClient.Get(ctx, req.NamespacedName, updatedValkey)).To(Succeed())
				Expect(updatedValkey.Status).NotTo(BeNil())
				Expect(updatedValkey.Status.ReconcilePhase).To(Equal("Completed"))
			})
		})
	})
})

// Helper function to find a condition by type
func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
