package controller

import (
	"github.com/nais/pgrator/pkg/api"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("getEngine", func() {
	DescribeTable("resolves engine from annotations",
		func(annotations map[string]string, wantEngine string, wantErr bool) {
			obj := &data_nais_io_v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
				},
			}
			got, err := getEngine(obj)
			if wantErr {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(got).To(Equal(wantEngine))
			}
		},
		Entry("no annotations defaults to zalando", nil, api.EngineZalando, false),
		Entry("empty annotation defaults to zalando", engineAnnotations("", ""), api.EngineZalando, false),
		Entry("explicit zalando", engineAnnotations("", "zalando"), api.EngineZalando, false),
		Entry("explicit cnpg", engineAnnotations("", "cnpg"), api.EngineCNPG, false),
		Entry("unknown value returns error", engineAnnotations("", "cockroachdb"), "", true),
		Entry("empty string value defaults to zalando", map[string]string{api.EngineAnnotation: ""}, api.EngineZalando, false),
		Entry("active-engine takes precedence over engine annotation",
			engineAnnotations(api.EngineCNPG, api.EngineZalando),
			api.EngineCNPG, false),
		Entry("active-engine used even if engine annotation is removed",
			engineAnnotations(api.EngineCNPG, ""),
			api.EngineCNPG, false),
		Entry("invalid active-engine returns error",
			engineAnnotations("invalid", ""),
			"", true),
	)
})

var _ = Describe("validateEngineImmutability", func() {
	DescribeTable("validates engine immutability",
		func(annotations map[string]string, engine string, wantErr bool) {
			obj := &data_nais_io_v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
				},
			}
			err := validateEngineImmutability(obj, engine)
			if wantErr {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("no active-engine annotation allows any engine (first reconcile)", nil, api.EngineCNPG, false),
		Entry("matching engine is allowed",
			engineAnnotations(api.EngineZalando, ""), api.EngineZalando, false),
		Entry("cnpg to cnpg is allowed",
			engineAnnotations(api.EngineCNPG, ""), api.EngineCNPG, false),
		Entry("zalando to cnpg is rejected",
			engineAnnotations(api.EngineZalando, ""), api.EngineCNPG, true),
		Entry("cnpg to zalando is rejected",
			engineAnnotations(api.EngineCNPG, ""), api.EngineZalando, true),
		Entry("empty active-engine allows first choice",
			map[string]string{api.ActiveEngineAnnotation: ""}, api.EngineCNPG, false),
	)
})

var _ = Describe("validateVersionForEngine", func() {
	DescribeTable("validates version for engine",
		func(majorVersion, engine string, wantErr bool, errContains string) {
			err := validateVersionForEngine(majorVersion, engine)
			if wantErr {
				Expect(err).To(HaveOccurred())
				if errContains != "" {
					Expect(err.Error()).To(ContainSubstring(errContains))
				}
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("cnpg with version 18 is valid", "18", api.EngineCNPG, false, ""),
		Entry("cnpg with version 19 is valid", "19", api.EngineCNPG, false, ""),
		Entry("cnpg with version 17 is rejected", "17", api.EngineCNPG, true, "cnpg engine requires majorVersion >= 18"),
		Entry("cnpg with version 16 is rejected", "16", api.EngineCNPG, true, "cnpg engine requires majorVersion >= 18"),
		Entry("zalando with version 16 is valid", "16", api.EngineZalando, false, ""),
		Entry("zalando with version 17 is valid", "17", api.EngineZalando, false, ""),
		Entry("zalando with version 18 is rejected", "18", api.EngineZalando, true, "zalando engine only supports majorVersion 16 or 17"),
		Entry("cnpg with invalid version returns error", "abc", api.EngineCNPG, true, "invalid major version"),
	)
})

// Describe the full engine resolution + validation pipeline as experienced by real users.
// This is the backward-compat safety net.
var _ = Describe("engine selection and validation pipeline", func() {
	DescribeTable("full pipeline",
		func(annotations map[string]string, majorVersion string, wantErr bool, errContains string) {
			obj := &data_nais_io_v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
				},
				Spec: data_nais_io_v1.PostgresSpec{
					Cluster: data_nais_io_v1.PostgresCluster{
						MajorVersion: majorVersion,
					},
				},
			}

			engine, err := getEngine(obj)
			if err != nil {
				Expect(wantErr).To(BeTrue(), "unexpected getEngine error: %v", err)
				if errContains != "" {
					Expect(err.Error()).To(ContainSubstring(errContains))
				}
				return
			}

			err = validateEngineImmutability(obj, engine)
			if err != nil {
				Expect(wantErr).To(BeTrue(), "unexpected immutability error: %v", err)
				if errContains != "" {
					Expect(err.Error()).To(ContainSubstring(errContains))
				}
				return
			}

			err = validateVersionForEngine(obj.Spec.Cluster.MajorVersion, engine)
			if err != nil {
				Expect(wantErr).To(BeTrue(), "unexpected version error: %v", err)
				if errContains != "" {
					Expect(err.Error()).To(ContainSubstring(errContains))
				}
				return
			}

			Expect(wantErr).To(BeFalse(), "expected error but validation pipeline succeeded")
		},
		// Backward compatibility: existing users without annotations
		Entry("no annotations with v16 defaults to zalando and succeeds", nil, "16", false, ""),
		Entry("no annotations with v17 defaults to zalando and succeeds", nil, "17", false, ""),
		Entry("empty annotations with v16 defaults to zalando and succeeds", engineAnnotations("", ""), "16", false, ""),
		Entry("empty annotations with v17 defaults to zalando and succeeds", engineAnnotations("", ""), "17", false, ""),
		// Existing user tries v18 without engine annotation (should fail)
		Entry("no annotations with v18 defaults to zalando and is rejected",
			nil, "18", true, "zalando engine only supports majorVersion 16 or 17"),
		// Existing zalando user with active-engine set (post first reconcile)
		Entry("active-engine zalando with v16 succeeds",
			engineAnnotations(api.EngineZalando, ""), "16", false, ""),
		Entry("active-engine zalando with v17 succeeds",
			engineAnnotations(api.EngineZalando, ""), "17", false, ""),
		Entry("active-engine zalando with v18 is rejected",
			engineAnnotations(api.EngineZalando, ""), "18", true, "zalando engine only supports majorVersion 16 or 17"),
		// New CNPG user
		Entry("engine cnpg with v18 succeeds",
			engineAnnotations("", api.EngineCNPG), "18", false, ""),
		Entry("engine cnpg with v17 is rejected",
			engineAnnotations("", api.EngineCNPG), "17", true, "cnpg engine requires majorVersion >= 18"),
		// Active-engine takes precedence: user annotation overridden
		Entry("active-engine zalando overrides engine cnpg annotation",
			engineAnnotations(api.EngineZalando, api.EngineCNPG), "17", false, ""),
		Entry("active-engine cnpg with v18 succeeds even without engine annotation",
			engineAnnotations(api.EngineCNPG, ""), "18", false, ""),
	)
})

func engineAnnotations(activeEngine, engine string) map[string]string {
	annotations := make(map[string]string)
	if activeEngine != "" {
		annotations[api.ActiveEngineAnnotation] = activeEngine
	}
	if engine != "" {
		annotations[api.EngineAnnotation] = engine
	}
	return annotations
}
