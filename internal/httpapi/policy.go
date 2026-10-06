package httpapi

import (
	"net/http"
	"strconv"

	"example.com/paleo-prep-station/internal/contracts"
)

func (api *API) getHandlingPolicy(w http.ResponseWriter, r *http.Request) {
	state, err := api.service.GetPolicyState(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (api *API) getPolicyVersion(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version <= 0 {
		writeError(w, contracts.Invalid("invalid_policy_version", "政策版本号必须是正整数"))
		return
	}
	value, err := api.service.GetPolicyVersion(r.Context(), version)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *API) savePolicyDraft(w http.ResponseWriter, r *http.Request) {
	var request contracts.SavePolicyDraftRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := api.service.SavePolicyDraft(r.Context(), request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *API) publishPolicy(w http.ResponseWriter, r *http.Request) {
	var request contracts.PublishPolicyRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := api.service.PublishPolicy(r.Context(), request)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", "/policies/handling/versions/"+strconv.Itoa(value.Version))
	writeJSON(w, http.StatusCreated, value)
}
