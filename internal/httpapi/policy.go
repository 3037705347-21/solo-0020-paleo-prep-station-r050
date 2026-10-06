package httpapi

import (
	"net/http"
	"strconv"

	"example.com/paleo-prep-station/internal/contracts"
)

func (api *API) getPolicyDraft(w http.ResponseWriter, r *http.Request) {
	view, err := api.service.GetPolicyDraft(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (api *API) putPolicyRule(w http.ResponseWriter, r *http.Request) {
	var request contracts.RestrictionRuleRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	rule, err := api.service.PutPolicyRule(r.Context(), request)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", "/policy/rules/"+rule.MatrixKind+"/"+string(rule.RiskLevel))
	writeJSON(w, http.StatusOK, rule)
}

func (api *API) deletePolicyRule(w http.ResponseWriter, r *http.Request) {
	err := api.service.DeletePolicyRule(
		r.Context(),
		r.PathValue("matrix"),
		r.PathValue("risk"),
	)
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) publishPolicy(w http.ResponseWriter, r *http.Request) {
	var request struct {
		PublishedBy string `json:"published_by"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	version, err := api.service.PublishPolicy(r.Context(), request.PublishedBy)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", "/policy/versions/"+strconv.Itoa(version.Number))
	writeJSON(w, http.StatusCreated, version)
}

func (api *API) listPolicyVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := api.service.ListPolicyVersions(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": versions})
}

func (api *API) getPolicyVersion(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || number < 1 {
		writeError(w, contracts.Invalid("invalid_policy_version", "政策版本号必须是正整数"))
		return
	}
	version, err := api.service.GetPolicyVersion(r.Context(), number)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, version)
}
