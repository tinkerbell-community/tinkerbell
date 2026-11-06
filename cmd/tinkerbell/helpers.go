package main

import (
	"maps"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/tinkerbell/tinkerbell/cmd/tinkerbell/flag"
	"github.com/tinkerbell/tinkerbell/pkg/backend/kube"
	"k8s.io/client-go/rest"
)

const serviceAccountNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

func ternary[T any](condition bool, valueIfTrue, valueIfFalse T) T {
	if condition {
		return valueIfTrue
	}
	return valueIfFalse
}

func numEnabled(globals *flag.GlobalConfig) int {
	n := 0
	if globals.EnableSmee {
		n++
	}
	if globals.EnableTootles {
		n++
	}
	if globals.EnableTinkServer {
		n++
	}
	if globals.EnableTinkController {
		n++
	}
	if globals.EnableRufio {
		n++
	}
	if globals.EnableSecondStar {
		n++
	}
	if globals.EnableUI {
		n++
	}
	return n
}

// enabledIndexes returns the indexes the enabled components need. While
// components move from v1alpha1 to v1alpha2 each may read either version, so
// every v1alpha1 index comes with its v1alpha2 counterparts.
func enabledIndexes(smeeEnabled, tootlesEnabled, tinkServerEnabled, secondStarEnabled bool) map[kube.IndexType]kube.Index {
	idxs := make(map[kube.IndexType]kube.Index)

	if smeeEnabled {
		maps.Copy(idxs, flag.KubeIndexesSmee)
	}
	if tootlesEnabled {
		maps.Copy(idxs, flag.KubeIndexesTootles)
	}
	if tinkServerEnabled {
		maps.Copy(idxs, flag.KubeIndexesTinkServer)
	}
	if secondStarEnabled {
		maps.Copy(idxs, flag.KubeIndexesSecondStar)
	}
	for _, t := range slices.Collect(maps.Keys(idxs)) {
		for _, counterpart := range kube.V1Alpha2Counterparts(t) {
			idxs[counterpart] = kube.Indexes[counterpart]
		}
	}

	return idxs
}

// normalizeURLPrefix ensures a URL prefix is valid for use with http.ServeMux.
// It trims whitespace, ensures a leading "/", cleans the path (collapsing repeated
// slashes, resolving ".." etc.), and ensures a trailing "/" so the mux matches all
// sub-paths.
func normalizeURLPrefix(prefix string) string {
	pattern := strings.TrimSpace(prefix)
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	pattern = path.Clean(pattern)
	if !strings.HasSuffix(pattern, "/") {
		pattern += "/"
	}
	return pattern
}

// podNamespace returns the namespace of the pod Tinkerbell runs in, or empty
// when not running in a pod.
func podNamespace() string {
	b, err := os.ReadFile(serviceAccountNamespaceFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// inCluster checks if we are running in cluster.
func inCluster() bool {
	if _, err := rest.InClusterConfig(); err == nil {
		return true
	}
	return false
}

// With TLS, plain HTTP only redirects to HTTPS unless the redirect is disabled.
func metadataEndpoint(globals *flag.GlobalConfig, tlsEnabled bool) flag.MetadataEndpoint {
	md := flag.MetadataEndpoint{Enabled: globals.EnableTootles, PublicIP: globals.PublicIP, Scheme: flag.SchemeHTTP, Port: globals.HTTPPort}
	if tlsEnabled && !globals.TLS.DisableHTTPToHTTPSRedirect {
		md.Scheme, md.Port = flag.SchemeHTTPS, globals.HTTPSPort
	}
	return md
}
