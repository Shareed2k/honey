package k8sproxy

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMain is defined once for the package in proxy_test.go.

func TestParseRequestInfo(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		rawPath  string
		rawQuery string
		want     RequestInfo
	}{
		{
			name:    "list namespaced pods",
			method:  http.MethodGet,
			rawPath: "/api/v1/namespaces/default/pods",
			want: RequestInfo{
				Verb: "list", APIGroup: "", APIVersion: "v1",
				Resource: "pods", Namespace: "default",
			},
		},
		{
			name:    "get named pod",
			method:  http.MethodGet,
			rawPath: "/api/v1/namespaces/default/pods/web-0",
			want: RequestInfo{
				Verb: "get", APIGroup: "", APIVersion: "v1",
				Resource: "pods", Namespace: "default", Name: "web-0",
			},
		},
		{
			name:    "exec subresource is create",
			method:  http.MethodPost,
			rawPath: "/api/v1/namespaces/default/pods/web-0/exec",
			want: RequestInfo{
				Verb: "create", APIGroup: "", APIVersion: "v1",
				Resource: "pods", Namespace: "default", Name: "web-0", Subresource: "exec",
			},
		},
		{
			name:    "apps group deployments list",
			method:  http.MethodGet,
			rawPath: "/apis/apps/v1/namespaces/default/deployments",
			want: RequestInfo{
				Verb: "list", APIGroup: "apps", APIVersion: "v1",
				Resource: "deployments", Namespace: "default",
			},
		},
		{
			name:    "cluster-scoped nodes list",
			method:  http.MethodGet,
			rawPath: "/api/v1/nodes",
			want: RequestInfo{
				Verb: "list", APIGroup: "", APIVersion: "v1",
				Resource: "nodes",
			},
		},
		{
			name:    "delete named pod",
			method:  http.MethodDelete,
			rawPath: "/api/v1/namespaces/default/pods/web-0",
			want: RequestInfo{
				Verb: "delete", APIGroup: "", APIVersion: "v1",
				Resource: "pods", Namespace: "default", Name: "web-0",
			},
		},
		{
			name:     "watch overrides list",
			method:   http.MethodGet,
			rawPath:  "/api/v1/namespaces/default/pods",
			rawQuery: "watch=true",
			want: RequestInfo{
				Verb: "watch", APIGroup: "", APIVersion: "v1",
				Resource: "pods", Namespace: "default",
			},
		},
		{
			name:    "cluster-scoped named node get",
			method:  http.MethodGet,
			rawPath: "/api/v1/nodes/worker-1",
			want: RequestInfo{
				Verb: "get", APIGroup: "", APIVersion: "v1",
				Resource: "nodes", Name: "worker-1",
			},
		},
		{
			name:    "update named resource",
			method:  http.MethodPut,
			rawPath: "/api/v1/namespaces/default/pods/web-0",
			want: RequestInfo{
				Verb: "update", APIGroup: "", APIVersion: "v1",
				Resource: "pods", Namespace: "default", Name: "web-0",
			},
		},
		{
			name:    "patch named resource",
			method:  http.MethodPatch,
			rawPath: "/api/v1/namespaces/default/pods/web-0",
			want: RequestInfo{
				Verb: "patch", APIGroup: "", APIVersion: "v1",
				Resource: "pods", Namespace: "default", Name: "web-0",
			},
		},
		{
			name:    "create namespaced collection",
			method:  http.MethodPost,
			rawPath: "/api/v1/namespaces/default/pods",
			want: RequestInfo{
				Verb: "create", APIGroup: "", APIVersion: "v1",
				Resource: "pods", Namespace: "default",
			},
		},
		{
			name:    "status subresource",
			method:  http.MethodPut,
			rawPath: "/apis/apps/v1/namespaces/default/deployments/web/status",
			want: RequestInfo{
				Verb: "update", APIGroup: "apps", APIVersion: "v1",
				Resource: "deployments", Namespace: "default", Name: "web", Subresource: "status",
			},
		},
		{
			name:    "log subresource GET is get (has name)",
			method:  http.MethodGet,
			rawPath: "/api/v1/namespaces/default/pods/web-0/log",
			want: RequestInfo{
				Verb: "get", APIGroup: "", APIVersion: "v1",
				Resource: "pods", Namespace: "default", Name: "web-0", Subresource: "log",
			},
		},
		{
			name:    "opaque path yields verb only",
			method:  http.MethodGet,
			rawPath: "/healthz",
			want:    RequestInfo{Verb: "list"},
		},
		{
			name:    "empty path never panics",
			method:  http.MethodGet,
			rawPath: "",
			want:    RequestInfo{Verb: "list"},
		},
		{
			name:    "namespaces with no namespace name never panics",
			method:  http.MethodGet,
			rawPath: "/api/v1/namespaces",
			want: RequestInfo{
				Verb: "list", APIVersion: "v1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRequestInfo(tt.method, tt.rawPath, tt.rawQuery)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseRequestInfo_NeverPanics(t *testing.T) {
	inputs := []struct{ method, path, query string }{
		{"", "", ""},
		{http.MethodGet, "/", ""},
		{http.MethodGet, "///", ""},
		{http.MethodGet, "/api", ""},
		{http.MethodGet, "/apis", ""},
		{http.MethodGet, "/apis/apps", ""},
		{http.MethodGet, "/api/v1/namespaces/", ""},
		{"WEIRD", "/api/v1/namespaces/default/pods", "watch=%zz"},
	}
	for _, in := range inputs {
		require.NotPanics(t, func() {
			parseRequestInfo(in.method, in.path, in.query)
		})
	}
}
