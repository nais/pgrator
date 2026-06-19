package controller

import (
	storage_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("copyCnrmAnnotations", func() {
	DescribeTable("copies CNRM-prefixed annotations from existing to target",
		func(existingAnnotations map[string]string, targetAnnotations map[string]string, expectedAnnotations map[string]string) {
			existing := &unstructured.Unstructured{}
			existing.SetAnnotations(existingAnnotations)

			target := &storage_cnrm_cloud_google_com_v1beta1.StorageBucket{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: targetAnnotations,
				},
			}

			copyCnrmAnnotations(existing, target)

			Expect(target.GetAnnotations()).To(Equal(expectedAnnotations))
		},
		Entry("only CNRM annotations are all copied",
			map[string]string{
				"cnrm.cloud.google.com/project-id":      "my-project",
				"cnrm.cloud.google.com/state-into-spec": "merge",
			},
			nil,
			map[string]string{
				"cnrm.cloud.google.com/project-id":      "my-project",
				"cnrm.cloud.google.com/state-into-spec": "merge",
			},
		),
		Entry("only non-CNRM annotations are not copied",
			map[string]string{
				"app.kubernetes.io/name":    "my-app",
				"some-other-annotation.io/": "value",
			},
			nil,
			nil,
		),
		Entry("mix of CNRM and non-CNRM: only CNRM copied",
			map[string]string{
				"cnrm.cloud.google.com/project-id": "my-project",
				"app.kubernetes.io/name":           "my-app",
			},
			nil,
			map[string]string{
				"cnrm.cloud.google.com/project-id": "my-project",
			},
		),
		Entry("nil annotations on existing: no-op, no panic",
			nil,
			nil,
			nil,
		),
		Entry("pre-existing target annotations are preserved and CNRM merged in",
			map[string]string{
				"cnrm.cloud.google.com/project-id": "my-project",
			},
			map[string]string{
				"existing-annotation": "keep-me",
			},
			map[string]string{
				"existing-annotation":              "keep-me",
				"cnrm.cloud.google.com/project-id": "my-project",
			},
		),
		Entry("CNRM annotation with empty value is still copied",
			map[string]string{
				"cnrm.cloud.google.com/project-id": "",
			},
			nil,
			map[string]string{
				"cnrm.cloud.google.com/project-id": "",
			},
		),
	)
})
