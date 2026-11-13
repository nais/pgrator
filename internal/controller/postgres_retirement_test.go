package controller

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

const retiredPostgresBranchKind = "PostgresBranch"

func TestPublishedChartRetiresPostgresBranch(t *testing.T) {
	for _, path := range []string{"../../config/crd/bases/nais.io_postgresbranches.yaml", "../../charts/pgrator/templates/crd/nais.io_postgresbranches.yaml"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("retired CRD must not be published at %s: %v", path, err)
		}
	}
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatal("helm is required to validate the published retirement chart")
	}
	out, err := exec.Command(helm, "template", "retirement", "../../charts/pgrator").CombinedOutput()
	if err != nil {
		t.Fatalf("rendering chart: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "postgresbranches") || strings.Contains(string(out), "kind: "+retiredPostgresBranchKind) {
		t.Fatal("rendered chart still publishes the retired branch CRD or RBAC")
	}
}

func TestE2ERetirementChecksCRDInsteadOfRemovedKind(t *testing.T) {
	data, err := os.ReadFile("../../tests/e2e/postgres-basic/chainsaw-test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var scenario struct {
		Spec struct {
			Steps []struct {
				Try []struct {
					Error *struct {
						Resource struct {
							metav1.TypeMeta `json:",inline"`
							Metadata        metav1.ObjectMeta `json:"metadata"`
						} `json:"resource"`
					} `json:"error"`
				} `json:"try"`
			} `json:"steps"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(data, &scenario); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, step := range scenario.Spec.Steps {
		for _, operation := range step.Try {
			if operation.Error == nil {
				continue
			}
			r := operation.Error.Resource
			if r.Kind == retiredPostgresBranchKind {
				t.Fatal("Chainsaw cannot check an absent object through a removed GVK; assert that the CRD is absent instead")
			}
			found = found || r.APIVersion == "apiextensions.k8s.io/v1" && r.Kind == "CustomResourceDefinition" && r.Metadata.Name == "postgresbranches.nais.io"
		}
	}
	if !found {
		t.Fatal("E2E must assert that the retired branch CRD is absent")
	}
}
