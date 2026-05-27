package controller

import (
	"github.com/nais/pgrator/pkg/api"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Engine", func() {
	When("Engine is not set in Status", func() {
		DescribeTable("getEngine resolves engine from annotations",
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
			Entry("empty annotation defaults to zalando", engineAnnotation(""), api.EngineZalando, false),
			Entry("explicit zalando", engineAnnotation("zalando"), api.EngineZalando, false),
			Entry("explicit cnpg", engineAnnotation("cnpg"), api.EngineCNPG, false),
			Entry("unknown value returns error", engineAnnotation("cockroachdb"), "", true),
		)
	})

	When("Engine is set in Status", func() {
		DescribeTable("getEngine always uses status",
			func(annotations map[string]string) {
				obj := &data_nais_io_v1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: annotations,
					},
					Status: &data_nais_io_v1.PostgresStatus{
						Engine: api.EngineCNPG,
					},
				}
				got, err := getEngine(obj)
				Expect(err).NotTo(HaveOccurred())
				Expect(got).To(Equal(api.EngineCNPG))
			},
			Entry("no annotations", nil),
			Entry("empty annotation", engineAnnotation("")),
			Entry("explicit zalando", engineAnnotation("zalando")),
			Entry("explicit cnpg", engineAnnotation("cnpg")),
			Entry("unknown value in annotation", engineAnnotation("cockroachdb")),
		)

		DescribeTable("validateEngineImmutability validates engine immutability",
			func(activeEngine, selectedEngine string, wantErr bool) {
				obj := &data_nais_io_v1.Postgres{
					Status: &data_nais_io_v1.PostgresStatus{
						Engine: activeEngine,
					},
				}
				err := validateEngineImmutability(obj, selectedEngine)
				if wantErr {
					Expect(err).To(HaveOccurred())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
			},
			Entry("no active engine allows any engine (first reconcile)", "", api.EngineCNPG, false),
			Entry("zalando to zalando is allowed", api.EngineZalando, api.EngineZalando, false),
			Entry("cnpg to cnpg is allowed", api.EngineCNPG, api.EngineCNPG, false),
			Entry("zalando to cnpg is rejected", api.EngineZalando, api.EngineCNPG, true),
			Entry("cnpg to zalando is rejected", api.EngineCNPG, api.EngineZalando, true),
		)
	})
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
		func(annotations map[string]string, status *data_nais_io_v1.PostgresStatus, majorVersion string, wantErr bool, errContains string) {
			obj := &data_nais_io_v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
				},
				Spec: data_nais_io_v1.PostgresSpec{
					Cluster: data_nais_io_v1.PostgresCluster{
						MajorVersion: majorVersion,
					},
				},
				Status: status,
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
		Entry("no annotations and no status with v16 defaults to zalando and succeeds", nil, nil, "16", false, ""),
		Entry("no annotations and no status with v17 defaults to zalando and succeeds", nil, nil, "17", false, ""),
		Entry("empty annotations and no status with v16 defaults to zalando and succeeds", engineAnnotation(""), nil, "16", false, ""),
		Entry("empty annotations and no status with v17 defaults to zalando and succeeds", engineAnnotation(""), nil, "17", false, ""),
		// Existing user tries v18 without engine annotation (should fail)
		Entry("no annotations and no status with v18 defaults to zalando and is rejected",
			nil, nil, "18", true, "zalando engine only supports majorVersion 16 or 17"),
		// Existing zalando user with active-engine set (post first reconcile)
		Entry("zalando in status with v16 succeeds",
			engineAnnotation(""), engineStatus(api.EngineZalando), "16", false, ""),
		Entry("zalando in status with v17 succeeds",
			engineAnnotation(""), engineStatus(api.EngineZalando), "17", false, ""),
		Entry("zalando in status with v18 is rejected",
			engineAnnotation(""), engineStatus(api.EngineZalando), "18", true, "zalando engine only supports majorVersion 16 or 17"),
		// New CNPG user
		Entry("engine cnpg with v18 succeeds",
			engineAnnotation(api.EngineCNPG), nil, "18", false, ""),
		Entry("engine cnpg with v17 is rejected",
			engineAnnotation(api.EngineCNPG), nil, "17", true, "cnpg engine requires majorVersion >= 18"),
		// Active-engine takes precedence: user annotation overridden
		Entry("zalando in status overrides engine cnpg annotation",
			engineAnnotation(api.EngineCNPG), engineStatus(api.EngineZalando), "17", false, ""),
		Entry("cnpg in status with v18 succeeds even without engine annotation",
			engineAnnotation(""), engineStatus(api.EngineCNPG), "18", false, ""),
	)
})

func engineStatus(engine string) *data_nais_io_v1.PostgresStatus {
	return &data_nais_io_v1.PostgresStatus{
		Engine: engine,
	}
}

func engineAnnotation(engine string) map[string]string {
	annotations := make(map[string]string)
	if engine != "" {
		annotations[api.EngineAnnotation] = engine
	}
	return annotations
}
