package controller

import (
	"context"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/pkg/api"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	core_v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("Prepare", func() {
	var scheme *runtime.Scheme
	var teamNamespace *core_v1.Namespace

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(core_v1.AddToScheme(scheme)).To(Succeed())
		Expect(data_nais_io_v1.AddToScheme(scheme)).To(Succeed())

		teamNamespace = &core_v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-team",
				Labels: map[string]string{
					ProjectIDLabel: "test-project",
				},
			},
		}
	})

	DescribeTable("stamps engine in status",
		func(annotations map[string]string, majorVersion, wantEngine string) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(teamNamespace.DeepCopy()).
				Build()

			reconciler := &PostgresReconciler{
				Config: &config.Config{},
			}

			obj := &data_nais_io_v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-db",
					Namespace:   "my-team",
					Annotations: annotations,
				},
				Spec: data_nais_io_v1.PostgresSpec{
					Cluster: data_nais_io_v1.PostgresCluster{
						MajorVersion: majorVersion,
						Resources: data_nais_io_v1.PostgresResources{
							DiskSize: resource.MustParse("10Gi"),
							Memory:   resource.MustParse("1Gi"),
						},
					},
				},
			}

			prep, _, err := reconciler.Prepare(context.Background(), fakeClient, obj)
			Expect(err).NotTo(HaveOccurred())
			Expect(prep.Engine).To(Equal(wantEngine))
		},
		Entry("no annotations selects zalando", nil, "16", api.EngineZalando),
		Entry("empty annotations selects zalando", map[string]string{}, "17", api.EngineZalando),
		Entry("explicit cnpg engine selects cnpg", map[string]string{api.EngineAnnotation: api.EngineCNPG}, "18", api.EngineCNPG),
		Entry("explicit zalando engine selects zalando", map[string]string{api.EngineAnnotation: api.EngineZalando}, "16", api.EngineZalando),
	)
})
