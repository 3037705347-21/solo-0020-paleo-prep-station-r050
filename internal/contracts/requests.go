package contracts

type RegisterSpecimenRequest struct {
	CatalogNumber string `json:"catalog_number"`
	FieldLabel    string `json:"field_label"`
	TaxonHint     string `json:"taxon_hint"`
	MatrixKind    string `json:"matrix_kind"`
	RiskLevel     string `json:"risk_level"`
	JacketNotes   string `json:"jacket_notes"`
}

// PlanParametersRequest carries the preparation choices restricted by the
// handling policy. PeelStrength is optional unless the matched rule caps it.
type PlanParametersRequest struct {
	Objective      string   `json:"objective"`
	Method         string   `json:"method"`
	Adhesive       string   `json:"adhesive"`
	TargetExposure int      `json:"target_exposure"`
	PeelStrength   *float64 `json:"peel_strength"`
}

type CreatePlanRequest = PlanParametersRequest

type RevisePlanRequest = PlanParametersRequest

type ApprovalRequest struct {
	ApprovedBy string `json:"approved_by"`
}

type ProgressRequest struct {
	Stage      string `json:"stage"`
	Percent    int    `json:"percent"`
	Note       string `json:"note"`
	RecordedBy string `json:"recorded_by"`
}

type ReleaseRequest struct {
	Purpose    string `json:"purpose"`
	Recipient  string `json:"recipient"`
	ApprovedBy string `json:"approved_by"`
}

// HandlingRuleRequest is one matrix/risk restriction row of a draft policy.
type HandlingRuleRequest struct {
	MatrixKind        string   `json:"matrix_kind"`
	RiskLevel         string   `json:"risk_level"`
	MaxPeelStrength   *float64 `json:"max_peel_strength"`
	AllowedAdhesives  []string `json:"allowed_adhesives"`
	MinTargetExposure int      `json:"min_target_exposure"`
	MaxTargetExposure int      `json:"max_target_exposure"`
	Notes             string   `json:"notes"`
}

type SavePolicyDraftRequest struct {
	// ExpectedRevision enables optimistic concurrency: when non-zero the save
	// only succeeds if the draft currently has exactly this revision.
	ExpectedRevision int                   `json:"expected_revision"`
	UpdatedBy        string                `json:"updated_by"`
	Rules            []HandlingRuleRequest `json:"rules"`
}

type PublishPolicyRequest struct {
	PublishedBy string `json:"published_by"`
}

type ReclassifySpecimenRequest struct {
	MatrixKind string `json:"matrix_kind"`
	RiskLevel  string `json:"risk_level"`
}
