// Package kubeapi answers questions about the Kubernetes API that Tinkerbell's
// services talk to.
package kubeapi

import (
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Served reports whether the API server serves the kind of obj, as the scheme
// of c maps it. The kinds that only exist in v1alpha1 (Template,
// WorkflowRuleSet, and the bmc Machine and Task) are not served once their CRDs
// are removed: controllers that watch them must not be started then, as they
// would never sync.
func Served(c client.Client, obj runtime.Object) (bool, error) {
	if _, err := c.IsObjectNamespaced(obj); err != nil {
		if meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
