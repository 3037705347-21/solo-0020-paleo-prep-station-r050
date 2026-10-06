package domain

import (
	"math"
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

// PlanParameters captures the preparation choices that handling policies
// restrict. Both creation and draft revision submit the same parameter set.
type PlanParameters struct {
	Objective      string
	Method         string
	Adhesive       string
	TargetExposure int
	PeelStrength   *float64
}

type PrepPlan struct {
	ID             string     `json:"id"`
	SpecimenID     string     `json:"specimen_id"`
	Objective      string     `json:"objective"`
	Method         string     `json:"method"`
	Adhesive       string     `json:"adhesive"`
	TargetExposure int        `json:"target_exposure"`
	PeelStrength   *float64   `json:"peel_strength,omitempty"`
	PolicyVersion  int        `json:"policy_version"`
	Status         PlanStatus `json:"status"`
	ApprovedBy     string     `json:"approved_by,omitempty"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty"`
	Version        int        `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// NewPrepPlan builds a draft plan. policyVersion is the published handling
// policy under which the parameters were already evaluated; it is snapshotted
// here so later policy releases never retroactively change this plan.
func NewPrepPlan(
	id string,
	specimen Specimen,
	parameters PlanParameters,
	policyVersion int,
	now time.Time,
) (PrepPlan, error) {
	if id == "" {
		return PrepPlan{}, contracts.Internal("missing_generated_id", "方案标识未生成", nil)
	}
	if policyVersion <= 0 {
		return PrepPlan{}, contracts.Internal("missing_policy_version", "方案未记录有效的限制政策版本", nil)
	}
	if !specimen.CanCreatePlan() {
		return PrepPlan{}, contracts.Conflict("specimen_not_plannable", "当前标本状态不能创建修复方案")
	}
	validated, err := ValidatePlanParameters(parameters)
	if err != nil {
		return PrepPlan{}, err
	}
	stamp := now.UTC()
	return PrepPlan{
		ID:             id,
		SpecimenID:     specimen.ID,
		Objective:      validated.Objective,
		Method:         validated.Method,
		Adhesive:       validated.Adhesive,
		TargetExposure: validated.TargetExposure,
		PeelStrength:   validated.PeelStrength,
		PolicyVersion:  policyVersion,
		Status:         PlanDraft,
		Version:        1,
		CreatedAt:      stamp,
		UpdatedAt:      stamp,
	}, nil
}

func ValidatePlanParameters(parameters PlanParameters) (PlanParameters, error) {
	goal, err := requiredText("修复目标", parameters.Objective, 200)
	if err != nil {
		return PlanParameters{}, err
	}
	approach, err := requiredText("修复方法", parameters.Method, 160)
	if err != nil {
		return PlanParameters{}, err
	}
	adhesiveValue, err := optionalText("黏结材料", parameters.Adhesive, 120)
	if err != nil {
		return PlanParameters{}, err
	}
	if err := validatePercent(parameters.TargetExposure); err != nil {
		return PlanParameters{}, contracts.Invalid("invalid_target_exposure", "目标暴露比例必须位于 0 到 100 之间")
	}
	if parameters.TargetExposure == 0 {
		return PlanParameters{}, contracts.Invalid("invalid_target_exposure", "目标暴露比例必须大于零")
	}
	var peel *float64
	if parameters.PeelStrength != nil {
		if math.IsNaN(*parameters.PeelStrength) || math.IsInf(*parameters.PeelStrength, 0) || *parameters.PeelStrength <= 0 {
			return PlanParameters{}, contracts.Invalid("invalid_peel_strength", "剥离强度必须是正数")
		}
		value := *parameters.PeelStrength
		peel = &value
	}
	return PlanParameters{
		Objective:      goal,
		Method:         approach,
		Adhesive:       adhesiveValue,
		TargetExposure: parameters.TargetExposure,
		PeelStrength:   peel,
	}, nil
}

func (p PrepPlan) IsOpen() bool {
	return p.Status == PlanDraft || p.Status == PlanApproved || p.Status == PlanActive
}

func (p PrepPlan) IsCompleted() bool {
	return p.Status == PlanCompleted
}

// Revise replaces the restricted parameters of a draft and snapshots the
// policy version used for the new evaluation. Approved and later plans are
// immutable with respect to parameters, so policy updates cannot reach them.
func (p *PrepPlan) Revise(parameters PlanParameters, policyVersion int, now time.Time) error {
	if p.Status != PlanDraft {
		return contracts.Conflict("plan_not_revisable", "只有草稿方案可以修改参数")
	}
	if policyVersion <= 0 {
		return contracts.Internal("missing_policy_version", "方案未记录有效的限制政策版本", nil)
	}
	validated, err := ValidatePlanParameters(parameters)
	if err != nil {
		return err
	}
	p.Objective = validated.Objective
	p.Method = validated.Method
	p.Adhesive = validated.Adhesive
	p.TargetExposure = validated.TargetExposure
	p.PeelStrength = validated.PeelStrength
	p.PolicyVersion = policyVersion
	p.Version++
	p.UpdatedAt = now.UTC()
	return nil
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
