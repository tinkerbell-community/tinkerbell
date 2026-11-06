package crd

import (
	apiv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func gvr(c *apiv1.CustomResourceDefinition, version string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: c.Spec.Group, Version: version, Resource: c.Spec.Names.Plural}
}

func isNotFound(err error) bool { return apierrors.IsNotFound(err) }

func isConflict(err error) bool { return apierrors.IsConflict(err) }
