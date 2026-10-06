package httpapi

import (
	"net/http"

	"example.com/paleo-prep-station/internal/contracts"
)

func (api *API) reclassifySpecimen(w http.ResponseWriter, r *http.Request) {
	var request contracts.ReclassifySpecimenRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	value, err := api.service.ReclassifySpecimen(r.Context(), r.PathValue("id"), request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
