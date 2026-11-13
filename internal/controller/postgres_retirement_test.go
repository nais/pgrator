package controller

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

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
	if strings.Contains(string(out), "postgresbranches") || strings.Contains(string(out), "kind: PostgresBranch") {
		t.Fatal("rendered chart still publishes the retired branch CRD or RBAC")
	}
}
