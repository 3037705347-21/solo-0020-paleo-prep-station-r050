package domain

import (
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type PlanStatus string

const (
	PlanDraft     PlanStatus = "draft"
	PlanApproved  PlanStatus = "approved"
	PlanActive    PlanStatus = "active"
	PlanCompleted PlanStatus = "completed"
	PlanCancelled PlanStatus = "cancelled"
)

type PrepPlan struct {
	ID               string     `json:"id"`
	SpecimenID       string     `json:"specimen_id"`
	Objective        string     `json:"objective"`
	Method           string     `json:"method"`
	Adhesive         string     `json:"adhesive"`
	PeelStrength     int        `json:"peel_strength"`
	TargetExposure   int        `json:"target_exposure"`
	Status           PlanStatus `json:"status"`
	PolicyVersion    int        `json:"policy_version"`
	PolicyMatrixKind string     `json:"policy_matrix_kind,omitempty"`
	PolicyRiskLevel  RiskLevel  `json:"policy_risk_level,omitempty"`
	ApprovedBy       string     `json:"approved_by,omitempty"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`
	Version          int        `json:"version"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func NewPrepPlan(
	id string,
	specimen Specimen,
	objective string,
	method string,
	adhesive string,
	peelStrength int,
	targetExposure int,
	now time.Time,
) (PrepPlan, error) {
	if id == "" {
		return PrepPlan{}, contracts.Internal("missing_generated_id", "方案标识未生成", nil)
	}
	if !specimen.CanCreatePlan() {
		return PrepPlan{}, contracts.Conflict("specimen_not_plannable", "当前标本状态不能创建修复方案")
	}
	params, err := validatePlanParameters(objective, method, adhesive, peelStrength, targetExposure)
	if err != nil {
		return PrepPlan{}, err
	}
	return PrepPlan{
		ID:             id,
		SpecimenID:     specimen.ID,
		Objective:      params.objective,
		Method:         params.method,
		Adhesive:       params.adhesive,
		PeelStrength:   params.peelStrength,
		TargetExposure: params.targetExposure,
		Status:         PlanDraft,
		Version:        1,
		CreatedAt:      now.UTC(),
		UpdatedAt:      now.UTC(),
	}, nil
}

type planParameters struct {
	objective      string
	method         string
	adhesive       string
	peelStrength   int
	targetExposure int
}

func validatePlanParameters(
	objective string,
	method string,
	adhesive string,
	peelStrength int,
	targetExposure int,
) (planParameters, error) {
	goal, err := requiredText("修复目标", objective, 200)
	if err != nil {
		return planParameters{}, err
	}
	approach, err := requiredText("修复方法", method, 160)
	if err != nil {
		return planParameters{}, err
	}
	adhesiveValue, err := optionalText("黏结材料", adhesive, 120)
	if err != nil {
		return planParameters{}, err
	}
	if peelStrength < 1 || peelStrength > 100 {
		return planParameters{}, contracts.Invalid("invalid_peel_strength", "剥离强度必须位于 1 到 100 之间")
	}
	if err := validatePercent(targetExposure); err != nil {
		return planParameters{}, contracts.Invalid("invalid_target_exposure", "目标暴露比例必须位于 0 到 100 之间")
	}
	if targetExposure == 0 {
		return planParameters{}, contracts.Invalid("invalid_target_exposure", "目标暴露比例必须大于零")
	}
	return planParameters{
		objective:      goal,
		method:         approach,
		adhesive:       adhesiveValue,
		peelStrength:   peelStrength,
		targetExposure: targetExposure,
	}, nil
}

// RestrictionParams exposes the parameter triple checked against policy.
func (p PrepPlan) RestrictionParams() PlanRestriction {
	return PlanRestriction{
		PeelStrength:   p.PeelStrength,
		Adhesive:       p.Adhesive,
		TargetExposure: p.TargetExposure,
	}
}

// StampPolicy records which published version and specimen classification the
// draft parameters were evaluated against. The stamp is only refreshed while
// the plan is still a draft, so later policy publications never rewrite it.
func (p *PrepPlan) StampPolicy(version PolicyVersion, specimen Specimen) {
	p.PolicyVersion = version.Number
	p.PolicyMatrixKind = specimen.MatrixKind
	p.PolicyRiskLevel = specimen.RiskLevel
}

// Revise updates draft parameters. Only a draft can change its parameters;
// approved plans have left the policy gate and must be cancelled/redone.
func (p *PrepPlan) Revise(
	objective string,
	method string,
	adhesive string,
	peelStrength int,
	targetExposure int,
	now time.Time,
) error {
	if p.Status != PlanDraft {
		return contracts.Conflict("plan_not_editable", "只有草稿方案可以修改参数")
	}
	params, err := validatePlanParameters(objective, method, adhesive, peelStrength, targetExposure)
	if err != nil {
		return err
	}
	p.Objective = params.objective
	p.Method = params.method
	p.Adhesive = params.adhesive
	p.PeelStrength = params.peelStrength
	p.TargetExposure = params.targetExposure
	p.PolicyVersion = 0
	p.PolicyMatrixKind = ""
	p.PolicyRiskLevel = ""
	p.Version++
	p.UpdatedAt = now.UTC()
	return nil
}

func (p PrepPlan) IsOpen() bool {
	return p.Status == PlanDraft || p.Status == PlanApproved || p.Status == PlanActive
}

func (p PrepPlan) IsCompleted() bool {
	return p.Status == PlanCompleted
}

func (p *PrepPlan) Approve(approvedBy string, now time.Time) error {
	if p.Status != PlanDraft {
		return contracts.Conflict("plan_not_approvable", "只有草稿方案可以审批")
	}
	approver, err := requiredText("审批人", approvedBy, 80)
	if err != nil {
		return err
	}
	stamp := now.UTC()
	p.Status = PlanApproved
	p.ApprovedBy = approver
	p.ApprovedAt = &stamp
	p.Version++
	p.UpdatedAt = stamp
	return nil
}

func (p *PrepPlan) Begin(now time.Time) error {
	if p.Status == PlanActive {
		return nil
	}
	if p.Status != PlanApproved {
		return contracts.Conflict("plan_not_active", "只有已审批方案可以开始制备")
	}
	p.Status = PlanActive
	p.Version++
	p.UpdatedAt = now.UTC()
	return nil
}

func (p *PrepPlan) Complete(now time.Time) error {
	if p.Status == PlanCompleted {
		return nil
	}
	if p.Status != PlanActive {
		return contracts.Conflict("plan_not_completable", "只有进行中的方案可以完成")
	}
	p.Status = PlanCompleted
	p.Version++
	p.UpdatedAt = now.UTC()
	return nil
}

func (p *PrepPlan) Cancel(now time.Time) error {
	if p.Status != PlanDraft {
		return contracts.Conflict("plan_not_cancellable", "只有草稿方案可以取消")
	}
	p.Status = PlanCancelled
	p.Version++
	p.UpdatedAt = now.UTC()
	return nil
}
