package webhttp

import (
	"context"
	"fmt"
	"strconv"

	"github.com/a-h/templ"
	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/ui/templates"
)

// listRequest is a request for a page of a resource list.
type listRequest struct {
	ctx    context.Context
	client *KubeClient
	// namespaces are the namespaces offered in the sidebar; only listed for full pages.
	namespaces []string
	namespace  string
	page       int
	perPage    int
}

// newListRequest reads a list request. A full page lists the namespaces and
// defaults to the user's only namespace; the htmx table endpoints use the
// namespace query as is. It reports false, having responded, when the
// request has no Kubernetes client.
func newListRequest(c *gin.Context, log logr.Logger, fullPage bool) (listRequest, bool) {
	client, err := GetKubeClientFromGinContext(c)
	if err != nil {
		log.Error(err, "Failed to get kube client")
		if !HandleAuthError(c, err, log) {
			c.String(500, "Internal server error")
		}
		return listRequest{}, false
	}
	r := listRequest{ctx: c.Request.Context(), client: client, namespace: c.Query("namespace")}
	if fullPage {
		r.namespaces = GetKubeNamespaces(r.ctx, c, client, log)
		r.namespace = GetSelectedNamespace(c, r.namespaces)
	}
	r.page, err = strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil {
		r.page = 1
	}
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", strconv.Itoa(DefaultItemsPerPage)))
	r.perPage = ValidateItemsPerPage(perPage)
	return r, true
}

// pageConfig returns the configuration of a page with the request's namespaces.
func pageConfig(c *gin.Context, namespaces []string) templates.PageConfig {
	return templates.PageConfig{BaseURL: GetBaseURL(c), Namespaces: namespaces}
}

// paginate returns the items on page, of perPage items each, and the
// pagination state of a list served at resourcePath into targetID.
func paginate[T any](items []T, page, perPage int, resourcePath, targetID string) ([]T, templates.PaginationData) {
	total := len(items)
	pages := max(1, (total+perPage-1)/perPage)
	page = min(max(page, 1), pages)
	start := min((page-1)*perPage, total)
	end := min(start+perPage, total)
	p := templates.PaginationData{
		CurrentPage:  page,
		TotalPages:   pages,
		TotalItems:   total,
		ItemsPerPage: perPage,
		ResourcePath: resourcePath,
		TargetID:     targetID,
	}
	if total > 0 {
		p.StartItem, p.EndItem = start+1, end
	}
	return items[start:end], p
}

// renderHTML renders component as the response.
func renderHTML(c *gin.Context, component templ.Component, log logr.Logger) {
	c.Header("Content-Type", "text/html")
	RenderComponent(c.Request.Context(), c.Writer, component, log)
}

// renderNotFound responds with the not found page of a resource.
func renderNotFound(c *gin.Context, log logr.Logger, namespaces []string, singular, plural, listPath, namespace, name string) {
	component := templates.NotFoundPage(pageConfig(c, namespaces), singular, name, namespace, listPath, plural, fmt.Sprintf("This %s may have been deleted.", singular))
	c.Header("Content-Type", "text/html")
	c.Status(404)
	RenderComponent(c.Request.Context(), c.Writer, component, log)
}

// detailRequest reads a request for a resource's detail page. It reports
// false, having responded, when the request has no Kubernetes client.
func detailRequest(c *gin.Context, log logr.Logger) (*KubeClient, []string, bool) {
	client, err := GetKubeClientFromGinContext(c)
	if err != nil {
		log.Error(err, "Failed to get kube client")
		if !HandleAuthError(c, err, log) {
			c.String(500, "Internal server error")
		}
		return nil, nil, false
	}
	return client, GetKubeNamespaces(c.Request.Context(), c, client, log), true
}
