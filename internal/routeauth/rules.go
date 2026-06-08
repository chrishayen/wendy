package routeauth

import (
	"net/http"

	"pacp/internal/transportauth"
)

func CatalogScopeRules() []transportauth.ScopeRule {
	componentMessage := "catalog operation requires a valid component credential"
	forbiddenMessage := "caller is not authorized for this catalog operation"
	return []transportauth.ScopeRule{
		{Method: http.MethodPost, Path: "/v1/catalog/manifests", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/catalog/export", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/catalog/services", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/catalog/services/{service_id}", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/catalog/capabilities", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/catalog/capabilities/{capability_id}", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/catalog/capabilities/{capability_id}/route", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/catalog/tags", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
	}
}

func JobScopeRules() []transportauth.ScopeRule {
	componentMessage := "job component operation requires a valid component credential"
	workerMessage := "job worker operation requires a valid runner credential"
	forbiddenMessage := "caller is not authorized for this job operation"
	return []transportauth.ScopeRule{
		{Method: http.MethodGet, Path: "/v1/jobs", Scopes: []string{"component", "worker"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/jobs/{job_id}", Scopes: []string{"component", "worker"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/jobs/{job_id}/policy-context", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/jobs/{job_id}/agent-projection", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/claim", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/heartbeat", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/complete", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/fail", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/cancel", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/jobs/{job_id}/logs", Scopes: []string{"component", "worker"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/jobs/{job_id}/logs", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
	}
}

func LeaseScopeRules() []transportauth.ScopeRule {
	componentMessage := "lease component operation requires a valid component credential"
	workerMessage := "lease worker operation requires a valid runner credential"
	mixedMessage := "lease operation requires a valid component or runner credential"
	forbiddenMessage := "caller is not authorized for this lease operation"
	return []transportauth.ScopeRule{
		{Method: http.MethodGet, Path: "/v1/resources", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/resources", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/resources/{resource_id}", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/resources/{resource_id}/inspection", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/lease-requests", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/lease-requests", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/lease-requests/{request_id}", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/lease-requests/{request_id}/cancel", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/leases/{lease_id}", Scopes: []string{"component", "worker"}, UnauthorizedMessage: mixedMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/leases/{lease_id}/heartbeat", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/leases/{lease_id}/release", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
	}
}

func ArtifactScopeRules() []transportauth.ScopeRule {
	componentMessage := "artifact component operation requires a valid component credential"
	workerMessage := "artifact worker operation requires a valid runner credential"
	forbiddenMessage := "caller is not authorized for this artifact operation"
	return []transportauth.ScopeRule{
		{Method: http.MethodPost, Path: "/v1/artifact-uploads", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/artifact-uploads/{upload_id}", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPut, Path: "/v1/artifact-uploads/{upload_id}/content", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/artifact-uploads/{upload_id}/complete", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/artifacts", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodPost, Path: "/v1/artifacts/register-local", Scopes: []string{"worker"}, UnauthorizedMessage: workerMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/artifacts/{artifact_id}", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/artifacts/{artifact_id}/policy-context", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
		{Method: http.MethodGet, Path: "/v1/artifacts/{artifact_id}/content", Scopes: []string{"component"}, UnauthorizedMessage: componentMessage, ForbiddenMessage: forbiddenMessage},
	}
}
