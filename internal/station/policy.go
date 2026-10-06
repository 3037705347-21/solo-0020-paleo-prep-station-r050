package station

import (
	"context"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

// PolicyDraftView is the readable snapshot of the draft the lead is editing.
type PolicyDraftView struct {
	Rules     []domain.RestrictionRule `json:"rules"`
	UpdatedAt time.Time                `json:"updated_at"`
}

// EffectiveRestrictionView answers: can this plan run on this specimen right
// now? It carries the resolved rule together with its version coordinates.
type EffectiveRestrictionView struct {
	PolicyVersion int                    `json:"policy_version"`
	MatrixKind    string                 `json:"matrix_kind"`
	RiskLevel     domain.RiskLevel       `json:"risk_level"`
	Rule          domain.RestrictionRule `json:"rule"`
}

// GetPolicyDraft reads the current draft, never creating published state.
func (s *Service) GetPolicyDraft(ctx context.Context) (PolicyDraftView, error) {
	if err := ctx.Err(); err != nil {
		return PolicyDraftView{}, err
	}
	var view PolicyDraftView
	err := s.repo.Read(func(m *repository.Mutation) error {
		draft := m.GetPolicyDraft(s.now())
		view = PolicyDraftView{
			Rules:     draft.SortedRules(),
			UpdatedAt: draft.UpdatedAt,
		}
		return nil
	})
	return view, err
}

// PutPolicyRule upserts one restriction entry keyed by matrix and risk level.
func (s *Service) PutPolicyRule(
	ctx context.Context,
	request contracts.RestrictionRuleRequest,
) (domain.RestrictionRule, error) {
	if err := ctx.Err(); err != nil {
		return domain.RestrictionRule{}, err
	}
	var saved domain.RestrictionRule
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		rule, err := domain.NewRestrictionRule(
			request.MatrixKind,
			request.RiskLevel,
			request.MaxPeelStrength,
			request.AllowedAdhesives,
			request.MaxTargetExposure,
			request.Notes,
		)
		if err != nil {
			return err
		}
		draft := m.GetPolicyDraft(s.now())
		draft.UpsertRule(rule, s.now())
		saved = rule
		return nil
	})
	return saved, err
}

// DeletePolicyRule removes one draft entry; published versions stay intact.
func (s *Service) DeletePolicyRule(ctx context.Context, matrixKind, riskLevel string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.repo.Mutate(func(m *repository.Mutation) error {
		draft := m.GetPolicyDraft(s.now())
		if !draft.RemoveRule(matrixKind, riskLevel, s.now()) {
			return contracts.NotFound("restriction_rule_not_found", "草稿中不存在该基质与风险等级的限制规则")
		}
		return nil
	})
}

// PublishPolicy snapshots the draft as the next immutable version.
func (s *Service) PublishPolicy(
	ctx context.Context,
	publishedBy string,
) (domain.PolicyVersion, error) {
	if err := ctx.Err(); err != nil {
		return domain.PolicyVersion{}, err
	}
	var published domain.PolicyVersion
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		draft := m.GetPolicyDraft(s.now())
		version, err := domain.PublishPolicy(*draft, m.PolicyVersions(), publishedBy, s.now())
		if err != nil {
			return err
		}
		m.AppendPolicyVersion(version)
		published = version
		return nil
	})
	return published, err
}

// ListPolicyVersions returns every published version in version order.
func (s *Service) ListPolicyVersions(ctx context.Context) ([]domain.PolicyVersion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var versions []domain.PolicyVersion
	err := s.repo.Read(func(m *repository.Mutation) error {
		versions = m.PolicyVersions()
		return nil
	})
	return versions, err
}

// GetPolicyVersion reads a specific published version.
func (s *Service) GetPolicyVersion(ctx context.Context, number int) (domain.PolicyVersion, error) {
	if err := ctx.Err(); err != nil {
		return domain.PolicyVersion{}, err
	}
	var version domain.PolicyVersion
	err := s.repo.Read(func(m *repository.Mutation) error {
		found, ok := m.PolicyVersionAt(number)
		if !ok {
			return contracts.NotFound("policy_version_not_found", "未找到限制政策版本")
		}
		version = found
		return nil
	})
	return version, err
}

// GetEffectiveRestriction resolves the rule governing a specimen's current
// classification in the latest published policy.
func (s *Service) GetEffectiveRestriction(
	ctx context.Context,
	specimenID string,
) (EffectiveRestrictionView, error) {
	if err := ctx.Err(); err != nil {
		return EffectiveRestrictionView{}, err
	}
	var view EffectiveRestrictionView
	err := s.repo.Read(func(m *repository.Mutation) error {
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
			return notFoundSpecimen(specimenID)
		}
		version, rule, err := resolveRestriction(m, specimen)
		if err != nil {
			return err
		}
		view = EffectiveRestrictionView{
			PolicyVersion: version.Number,
			MatrixKind:    specimen.MatrixKind,
			RiskLevel:     specimen.RiskLevel,
			Rule:          rule,
		}
		return nil
	})
	return view, err
}

// resolveRestriction applies the deterministic policy lookup inside a
// transaction: unclassified specimen, no published version, missing rule.
func resolveRestriction(
	m *repository.Mutation,
	specimen domain.Specimen,
) (domain.PolicyVersion, domain.RestrictionRule, error) {
	if !specimen.IsClassified() {
		return domain.PolicyVersion{}, domain.RestrictionRule{}, contracts.Conflict(
			"specimen_unclassified",
			"标本尚未确定风险等级，无法取得处置限制",
		)
	}
	version, ok := m.LatestPolicyVersion()
	if !ok {
		return domain.PolicyVersion{}, domain.RestrictionRule{}, contracts.Conflict(
			"restriction_policy_unpublished",
			"处置限制政策尚未发布任何版本",
		)
	}
	rule, ok := version.FindRule(specimen.MatrixKind, specimen.RiskLevel)
	if !ok {
		return domain.PolicyVersion{}, domain.RestrictionRule{}, contracts.Conflict(
			"restriction_rule_missing",
			"当前基质与风险等级在有效政策版本中没有对应限制规则",
		)
	}
	return version, rule, nil
}
