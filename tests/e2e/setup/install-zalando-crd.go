//go:build ignore

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
)

func main() {
	crd := acid_zalan_do_v1.PostgresCRD([]string{"all"})
	data, err := json.Marshal(crd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal CRD: %v\n", err)
		os.Exit(1)
	}

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to apply CRD: %v\n", err)
		os.Exit(1)
	}
}

