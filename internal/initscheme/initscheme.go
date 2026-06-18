package initscheme

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	aiven_v1alpha1 "github.com/nais/pgrator/internal/thirdparty/aiven/v1alpha1"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storage_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	"github.com/nais/pgrator/pkg/api/datav1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	monitoring_v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8s_scheme "k8s.io/client-go/kubernetes/scheme"
)

func InitScheme(scheme *runtime.Scheme) {
	utilruntime.Must(k8s_scheme.AddToScheme(scheme))
	utilruntime.Must(datav1.AddToScheme(scheme))
	utilruntime.Must(v1.AddToScheme(scheme))
	utilruntime.Must(iam_cnrm_cloud_google_com_v1beta1.AddToScheme(scheme))
	utilruntime.Must(storage_cnrm_cloud_google_com_v1beta1.AddToScheme(scheme))
	utilruntime.Must(monitoring_v1.AddToScheme(scheme))
	utilruntime.Must(acid_zalan_do_v1.AddToScheme(scheme))
	utilruntime.Must(aiven_v1alpha1.AddToScheme(scheme))
	utilruntime.Must(cnpgv1.AddToScheme(scheme))
	barmanv1.AddKnownTypes(scheme)
}
