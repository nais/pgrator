
#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
export ENVTEST_K8S_VERSION=$(grep "\sk8s.io/api\s" go.mod | awk -F. '{printf "1.%d", $3}')
