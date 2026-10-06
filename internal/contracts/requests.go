package contracts

type RegisterSpecimenRequest struct {
	CatalogNumber string `json:"catalog_number"`
	FieldLabel    string `json:"field_label"`
	TaxonHint     string `json:"taxon_hint"`
	MatrixKind    string `json:"matrix_kind"`
	RiskLevel     string `json:"risk_level,omitempty"`
	JacketNotes   string `json:"jacket_notes"`
}

type ClassificationRequest struct {
	MatrixKind string `json:"matrix_kind"`
	RiskLevel  string `json:"risk_level"`
}

type RestrictionRuleRequest struct {
	MatrixKind        string   `json:"matrix_kind"`
	RiskLevel         string   `json:"risk_level"`
	MaxPeelStrength   int      `json:"max_peel_strength"`
	AllowedAdhesives  []string `json:"allowed_adhesives"`
	MaxTargetExposure int      `json:"max_target_exposure"`
	Notes             string   `json:"notes,omitempty"`
}

type CreatePlanRequest struct {
	Objective      string `json:"objective"`
	Method         string `json:"method"`
	Adhesive       string `json:"adhesive"`
	PeelStrength   int    `json:"peel_strength"`
	TargetExposure int    `json:"target_exposure"`
}

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
