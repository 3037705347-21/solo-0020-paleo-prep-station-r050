package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

// PolicyState is the read model exposed to leads: the single editable draft
// plus the append-only history of published versions.
type PolicyState struct {
	Draft             *domain.PolicyDraft    `json:"draft,omitempty"`
	LatestVersion     *domain.PolicyVersion  `json:"latest_version,omitempty"`
	PublishedVersions []domain.PolicyVersion `json:"published_versions"`
}

func (s *Service) GetPolicyState(ctx context.Context) (PolicyState, error) {
	if err := ctx.Err(); err != nil {
		return PolicyState{}, err
	}
	var state PolicyState
	err := s.repo.Read(func(m *repository.Mutation) error {
		if draft, ok := m.GetPolicyDraft(); ok {
			state.Draft = &draft
		}
		if latest, ok := m.LatestPolicyVersion(); ok {
			state.LatestVersion = &latest
		}
		state.PublishedVersions = m.ListPolicyVersions()
		return nil
	})
	return state, err
}

func (s *Service) GetPolicyVersion(ctx context.Context, version int) (domain.PolicyVersion, error) {
	if err := ctx.Err(); err != nil {
		return domain.PolicyVersion{}, err
	}
	var value domain.PolicyVersion
	err := s.repo.Read(func(m *repository.Mutation) error {
		found, ok := m.GetPolicyVersion(version)
		if !ok {
			return notFoundPolicyVersion(version)
		}
		value = found
		return nil
	})
	return value, err
}

func (s *Service) SavePolicyDraft(
	ctx context.Context,
	request contracts.SavePolicyDraftRequest,
) (domain.PolicyDraft, error) {
	if err := ctx.Err(); err != nil {
		return domain.PolicyDraft{}, err
	}
	rules, err := buildRules(request.Rules)
	if err != nil {
		return domain.PolicyDraft{}, err
	}

	var saved domain.PolicyDraft
	err = s.repo.Mutate(func(m *repository.Mutation) error {
		revision := 1
		if current, ok := m.GetPolicyDraft(); ok {
			if request.ExpectedRevision != 0 && request.ExpectedRevision != current.Revision {
				return contracts.Conflict(
					"policy_draft_revision_conflict",
					"政策草稿已被其他维护人更新，请基于最新版本重新编辑",
				)
			}
			revision = current.Revision + 1
		} else if request.ExpectedRevision != 0 {
			return contracts.Conflict(
				"policy_draft_revision_conflict",
				"政策草稿已发布或不存在，请基于最新版本重新编辑",
			)
		}
		draft, err := domain.NewPolicyDraft(rules, request.UpdatedBy, revision, s.now())
		if err != nil {
			return err
		}
		m.PutPolicyDraft(draft)
		saved = draft
		return nil
	})
	return saved, err
}

func (s *Service) PublishPolicy(
	ctx context.Context,
	request contracts.PublishPolicyRequest,
) (domain.PolicyVersion, error) {
	if err := ctx.Err(); err != nil {
		return domain.PolicyVersion{}, err
	}

	var published domain.PolicyVersion
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		draft, ok := m.GetPolicyDraft()
		if !ok {
			// Covers both first-time publish with no draft and a repeated
			// publish request: the first publish consumes the draft.
			return contracts.Conflict("no_active_policy_draft", "没有待发布的政策草稿，不能重复发布")
		}
		nextVersion := 1
		if latest, exists := m.LatestPolicyVersion(); exists {
			nextVersion = latest.Version + 1
		}
		version, err := draft.Publish(nextVersion, request.PublishedBy, s.now())
		if err != nil {
			return err
		}
		m.AddPolicyVersion(version)
		m.ClearPolicyDraft()
		published = version
		return nil
	})
	return published, err
}

func buildRules(requests []contracts.HandlingRuleRequest) ([]domain.HandlingRule, error) {
	if len(requests) == 0 {
		return nil, contracts.Invalid("policy_without_rules", "政策至少需要包含一条限制")
	}
	rules := make([]domain.HandlingRule, 0, len(requests))
	for _, request := range requests {
		rule, err := domain.NewHandlingRule(
			request.MatrixKind,
			request.RiskLevel,
			request.MaxPeelStrength,
			request.AllowedAdhesives,
			request.MinTargetExposure,
			request.MaxTargetExposure,
			request.Notes,
		)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// evaluateAgainstCurrentPolicy resolves the restriction for the specimen's
// current classification and checks the parameters. It must run inside the
// same locked mutation as the plan write, so a concurrent policy publish and
// plan creation are serialized against one immutable version each.
func evaluateAgainstCurrentPolicy(
	m *repository.Mutation,
	specimen domain.Specimen,
	parameters domain.PlanParameters,
) (int, error) {
	version, ok := m.LatestPolicyVersion()
	if !ok {
		return 0, contracts.Conflict("handling_policy_missing", "尚未发布标本处置限制政策，不能评估修复方案")
	}
	rule, ok := version.RuleFor(specimen.MatrixKind, specimen.RiskLevel)
	if !ok {
		return 0, contracts.Conflict(
			"handling_rule_missing",
			"当前标本的基质和风险等级缺少生效限制，不能评估修复方案",
		)
	}
	if err := rule.AllowsPlan(parameters.Adhesive, parameters.TargetExposure, parameters.PeelStrength); err != nil {
		return 0, err
	}
	return version.Version, nil
}
