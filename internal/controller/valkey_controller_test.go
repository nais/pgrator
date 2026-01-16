package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/controller/resourcecreator"
	aiven_v1alpha1 "github.com/nais/pgrator/pkg/api/thirdparty/aiven/v1alpha1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
)

var _ = Describe("Valkey Controller", func() {
	Describe("ValkeyReconciler", func() {
		Describe("Prepare", func() {
			It("should return correct plan for valid config and valkey", func() {
				r := &ValkeyReconciler{
					Config: &config.Config{
						AivenProject: "test-project",
					},
				}
				valkey := &v1.Valkey{
					Spec: v1.ValkeySpec{
						Tier:   v1.ValkeyTierSingleNode,
						Memory: v1.ValkeyMemory4GB,
					},
				}

				preparedData, _, err := r.Prepare(context.Background(), nil, valkey)
				Expect(err).NotTo(HaveOccurred())
				Expect(preparedData.AivenPlan).To(Equal("startup-4"))
			})

			It("should return error when AIVEN_PROJECT is missing", func() {
				r := &ValkeyReconciler{
					Config: &config.Config{},
				}
				valkey := &v1.Valkey{
					Spec: v1.ValkeySpec{
						Tier:   v1.ValkeyTierSingleNode,
						Memory: v1.ValkeyMemory4GB,
					},
				}

				_, _, err := r.Prepare(context.Background(), nil, valkey)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("AIVEN_PROJECT"))
			})

			It("should return correct plan for high availability", func() {
				r := &ValkeyReconciler{
					Config: &config.Config{
						AivenProject: "test-project",
					},
				}
				valkey := &v1.Valkey{
					Spec: v1.ValkeySpec{
						Tier:   v1.ValkeyTierHighAvailability,
						Memory: v1.ValkeyMemory8GB,
					},
				}

				preparedData, _, err := r.Prepare(context.Background(), nil, valkey)
				Expect(err).NotTo(HaveOccurred())
				Expect(preparedData.AivenPlan).To(Equal("business-8"))
			})
		})

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
			It("should return nil", func() {
				r := &ValkeyReconciler{}
				Expect(r.OwnedTypes()).To(BeNil())
			})
		})

		Describe("AdditionalTypes", func() {
			It("should return Aiven Valkey and ServiceIntegration types", func() {
				r := &ValkeyReconciler{}
				got := r.AdditionalTypes()
				Expect(got).To(HaveLen(2))

				_, ok := got[0].(*aiven_v1alpha1.Valkey)
				Expect(ok).To(BeTrue())

				_, ok = got[1].(*aiven_v1alpha1.ServiceIntegration)
				Expect(ok).To(BeTrue())
			})
		})
	})

	Describe("CreateAivenValkeySpec", func() {
		const (
			testValkeyName = "my-valkey"
			testTeamName   = "my-team"
		)

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
			cfg := &config.Config{
				AivenProject:      "test-project",
				AivenProjectVPCID: "vpc-123",
				AivenTenantName:   "test-tenant",
			}

			result := resourcecreator.CreateAivenValkeySpec(valkey, cfg, "startup-4")

			Expect(result.Spec.Project).To(Equal("test-project"))
			Expect(result.Spec.Plan).To(Equal("startup-4"))
			Expect(result.Spec.ProjectVPCID).To(Equal("vpc-123"))
			Expect(result.Spec.TerminationProtection).NotTo(BeNil())
			Expect(*result.Spec.TerminationProtection).To(BeTrue())
			Expect(result.Spec.Tags["team"]).To(Equal(testTeamName))
			Expect(result.Spec.Tags["app"]).To(Equal(testValkeyName))
			Expect(result.Spec.Tags["managed-by"]).To(Equal("pgrator"))
			Expect(result.Spec.Tags["tenant"]).To(Equal("test-tenant"))
			Expect(result.Spec.ConnInfoSecretTarget).NotTo(BeNil())
			Expect(result.Spec.ConnInfoSecretTarget.Name).To(Equal(testValkeyName))
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
			cfg := &config.Config{
				AivenProject: "test-project",
			}

			result := resourcecreator.CreateAivenValkeySpec(valkey, cfg, "startup-4")

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
			cfg := &config.Config{
				AivenProject: "test-project",
			}

			result := resourcecreator.CreateAivenValkeySpec(valkey, cfg, "startup-4")

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
			cfg := &config.Config{
				AivenProject: "test-project",
			}

			result := resourcecreator.CreateAivenValkeySpec(valkey, cfg, "business-14")

			Expect(result.Spec.Plan).To(Equal("business-14"))
			Expect(result.Spec.UserConfig).NotTo(BeNil())
			Expect(*result.Spec.UserConfig.ValkeyMaxmemoryPolicy).To(Equal("allkeys-lfu"))
			Expect(*result.Spec.UserConfig.ValkeyNotifyKeyspaceEvents).To(Equal("Ex"))
		})

		It("should not include tenant tag when tenant is not set", func() {
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-tenant-valkey",
					Namespace: "no-tenant-team",
				},
				Spec: v1.ValkeySpec{
					Tier:   v1.ValkeyTierSingleNode,
					Memory: v1.ValkeyMemory4GB,
				},
			}
			cfg := &config.Config{
				AivenProject: "test-project",
			}

			result := resourcecreator.CreateAivenValkeySpec(valkey, cfg, "startup-4")

			_, hasTenant := result.Spec.Tags["tenant"]
			Expect(hasTenant).To(BeFalse())
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
			cfg := &config.Config{
				AivenProject: "test-project",
			}

			result := resourcecreator.CreateAivenValkeySpec(valkey, cfg, "startup-4")

			Expect(result.Labels["team"]).To(Equal("labeled-team"))
			Expect(result.Labels["custom-label"]).To(Equal("custom-value"))
			Expect(result.Labels["another"]).To(Equal("label"))
			Expect(result.Labels["valkey.nais.io/name"]).To(Equal("labeled-valkey"))
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
			cfg := &config.Config{
				AivenProject:                      "test-project",
				AivenMetricsDestinationEndpointID: "metrics-service",
			}

			result := resourcecreator.CreateServiceIntegrationSpec(
				valkey,
				cfg,
				"my-valkey-metrics",
				"metrics",
				"metrics-service",
			)

			Expect(result.Name).To(Equal("my-valkey-metrics"))
			Expect(result.Namespace).To(Equal("my-team"))
			Expect(result.Spec.Project).To(Equal("test-project"))
			Expect(result.Spec.IntegrationType).To(Equal("metrics"))
			Expect(result.Spec.SourceServiceName).To(Equal("my-valkey"))
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

	Describe("metricsIntegrationName", func() {
		It("should return valkey name with -metrics suffix", func() {
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-valkey",
				},
			}

			name := metricsIntegrationName(valkey)
			Expect(name).To(Equal("my-valkey-metrics"))
		})
	})

	Describe("MinimalAivenValkey", func() {
		It("should create minimal Aiven Valkey with correct metadata", func() {
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

			Expect(result.Name).To(Equal("test-valkey"))
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

			result := resourcecreator.MinimalServiceIntegration(valkey, "test-valkey-metrics")

			Expect(result.Name).To(Equal("test-valkey-metrics"))
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

		cfg := &config.Config{
			AivenProject: "test-project",
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

				result := resourcecreator.CreateAivenValkeySpec(valkey, cfg, "startup-4")

				Expect(result.Spec.UserConfig).NotTo(BeNil())
				Expect(result.Spec.UserConfig.ValkeyMaxmemoryPolicy).NotTo(BeNil())
				Expect(*result.Spec.UserConfig.ValkeyMaxmemoryPolicy).To(Equal(string(policy)))
			})
		}
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
