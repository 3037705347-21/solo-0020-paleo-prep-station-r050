package domain

import (
	"math"
	"sort"
	"strings"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// ParseRiskLevel accepts the public risk-level vocabulary. An empty value
// resolves to the default low risk classification.
func ParseRiskLevel(value string) (RiskLevel, error) {
	cleaned := strings.ToLower(strings.TrimSpace(value))
	if cleaned == "" {
		return RiskLow, nil
	}
	switch RiskLevel(cleaned) {
	case RiskLow, RiskMedium, RiskHigh:
		return RiskLevel(cleaned), nil
	default:
		return "", contracts.Invalid("invalid_risk_level", "风险等级只能是 low、medium 或 high")
	}
}

// HandlingRule is the immutable restriction that applies to one
// matrix-kind / risk-level classification within a published policy.
type HandlingRule struct {
	MatrixKind        string    `json:"matrix_kind"`
	RiskLevel         RiskLevel `json:"risk_level"`
	MaxPeelStrength   *float64  `json:"max_peel_strength,omitempty"`
	AllowedAdhesives  []string  `json:"allowed_adhesives"`
	MinTargetExposure int       `json:"min_target_exposure"`
	MaxTargetExposure int       `json:"max_target_exposure"`
	Notes             string    `json:"notes,omitempty"`
}

// NewHandlingRule validates a single restriction row. Adhesive names are
// trimmed, de-duplicated and sorted so equal input always yields an equal rule.
func NewHandlingRule(
	matrixKind string,
	riskLevel string,
	maxPeelStrength *float64,
	allowedAdhesives []string,
	minTargetExposure int,
	maxTargetExposure int,
	notes string,
) (HandlingRule, error) {
	matrix, err := requiredText("基质类型", matrixKind, 60)
	if err != nil {
		return HandlingRule{}, err
	}
	risk, err := ParseRiskLevel(riskLevel)
	if err != nil {
		return HandlingRule{}, err
	}
	var peel *float64
	if maxPeelStrength != nil {
		if math.IsNaN(*maxPeelStrength) || math.IsInf(*maxPeelStrength, 0) || *maxPeelStrength <= 0 {
			return HandlingRule{}, contracts.Invalid("invalid_peel_strength", "剥离强度上限必须是正数")
		}
		value := *maxPeelStrength
		peel = &value
	}
	adhesives, err := normalizeAdhesives(allowedAdhesives)
	if err != nil {
		return HandlingRule{}, err
	}
	if minTargetExposure < 0 || minTargetExposure > 100 {
		return HandlingRule{}, contracts.Invalid("invalid_target_exposure_range", "暴露比例下限必须位于 0 到 100 之间")
	}
	if maxTargetExposure < 0 || maxTargetExposure > 100 {
		return HandlingRule{}, contracts.Invalid("invalid_target_exposure_range", "暴露比例上限必须位于 0 到 100 之间")
	}
	if minTargetExposure > maxTargetExposure {
		return HandlingRule{}, contracts.Invalid("invalid_target_exposure_range", "暴露比例下限不能高于上限")
	}
	note, err := optionalText("限制说明", notes, 500)
	if err != nil {
		return HandlingRule{}, err
	}
	return HandlingRule{
		MatrixKind:        matrix,
		RiskLevel:         risk,
		MaxPeelStrength:   peel,
		AllowedAdhesives:  adhesives,
		MinTargetExposure: minTargetExposure,
		MaxTargetExposure: maxTargetExposure,
		Notes:             note,
	}, nil
}

func normalizeAdhesives(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		cleaned, err := requiredText("允许黏结材料", value, 120)
		if err != nil {
			// Empty entries inside the list are dropped instead of rejected, so
			// callers cannot accidentally smuggle a blank "any" adhesive.
			if strings.TrimSpace(value) == "" {
				continue
			}
			return nil, err
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		result = append(result, cleaned)
	}
	sort.Strings(result)
	return result, nil
}

func (r HandlingRule) key() string {
	return r.MatrixKind + "\x00" + string(r.RiskLevel)
}

// AllowsPlan evaluates one set of plan parameters against the rule. The
// result is purely a function of its inputs, so concurrent evaluation of the
// same published rule always returns the same answer.
func (r HandlingRule) AllowsPlan(
	adhesive string,
	targetExposure int,
	peelStrength *float64,
) error {
	if targetExposure < r.MinTargetExposure || targetExposure > r.MaxTargetExposure {
		return contracts.Invalid(
			"target_exposure_out_of_range",
			"目标暴露比例超出当前分类允许的区间",
		)
	}
	if adhesive != "" {
		if len(r.AllowedAdhesives) == 0 {
			return contracts.Invalid("adhesive_forbidden", "当前分类禁止使用任何黏结材料")
		}
		if !containsText(r.AllowedAdhesives, adhesive) {
			return contracts.Invalid("adhesive_not_allowed", "黏结材料不在当前分类允许范围内")
		}
	}
	if r.MaxPeelStrength != nil {
		if peelStrength == nil {
			return contracts.Invalid("peel_strength_required", "当前分类要求申报剥离强度")
		}
		if math.IsNaN(*peelStrength) || math.IsInf(*peelStrength, 0) || *peelStrength <= 0 {
			return contracts.Invalid("invalid_peel_strength", "剥离强度必须是正数")
		}
		diff := *peelStrength - *r.MaxPeelStrength
		if diff > 1e-9 {
			return contracts.Invalid("peel_strength_exceeds_limit", "剥离强度超过当前分类可承受上限")
		}
	}
	return nil
}

func containsText(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// PolicyVersion is a frozen, published restriction set. Once published it is
// never mutated; plans reference the version they were evaluated against.
type PolicyVersion struct {
	Version     int            `json:"version"`
	Rules       []HandlingRule `json:"rules"`
	PublishedBy string         `json:"published_by"`
	PublishedAt time.Time      `json:"published_at"`
}

// RuleFor returns the exact-match rule for the specimen's current
// classification. There is no implicit fallback across matrices or risks.
func (v PolicyVersion) RuleFor(matrixKind string, riskLevel RiskLevel) (HandlingRule, bool) {
	needle := HandlingRule{MatrixKind: strings.TrimSpace(matrixKind), RiskLevel: riskLevel}
	for _, rule := range v.Rules {
		if rule.key() == needle.key() {
			return rule, true
		}
	}
	return HandlingRule{}, false
}

// PolicyDraft is the working copy the lead preparator edits before publish.
// Revision increments on every replacement and supports optimistic
// concurrency control at publish time.
type PolicyDraft struct {
	Rules     []HandlingRule `json:"rules"`
	Revision  int            `json:"revision"`
	UpdatedBy string         `json:"updated_by"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// NewPolicyDraft validates the draft as a whole, including the requirement
// that each matrix/risk classification appears at most once.
func NewPolicyDraft(
	rules []HandlingRule,
	updatedBy string,
	revision int,
	now time.Time,
) (PolicyDraft, error) {
	editor, err := requiredText("维护人", updatedBy, 80)
	if err != nil {
		return PolicyDraft{}, err
	}
	if err := validateRuleSet(rules); err != nil {
		return PolicyDraft{}, err
	}
	stamp := now.UTC()
	return PolicyDraft{
		Rules:     cloneRules(rules),
		Revision:  revision,
		UpdatedBy: editor,
		UpdatedAt: stamp,
	}, nil
}

func validateRuleSet(rules []HandlingRule) error {
	if len(rules) == 0 {
		return contracts.Invalid("policy_without_rules", "政策至少需要包含一条限制")
	}
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if _, exists := seen[rule.key()]; exists {
			return contracts.Conflict("duplicate_rule", "同一基质和风险等级不能重复配置限制")
		}
		seen[rule.key()] = struct{}{}
	}
	return nil
}

func cloneRules(rules []HandlingRule) []HandlingRule {
	cloned := make([]HandlingRule, len(rules))
	copy(cloned, rules)
	return cloned
}

// Publish freezes the draft into an immutable version. The caller assigns
// the monotonically increasing version number while holding the repository
// lock, so concurrent publishes never alias one version.
func (d PolicyDraft) Publish(
	version int,
	publishedBy string,
	now time.Time,
) (PolicyVersion, error) {
	if version <= 0 {
		return PolicyVersion{}, contracts.Internal("missing_policy_version", "政策版本号未生成", nil)
	}
	publisher, err := requiredText("发布人", publishedBy, 80)
	if err != nil {
		return PolicyVersion{}, err
	}
	if err := validateRuleSet(d.Rules); err != nil {
		return PolicyVersion{}, err
	}
	return PolicyVersion{
		Version:     version,
		Rules:       cloneRules(d.Rules),
		PublishedBy: publisher,
		PublishedAt: now.UTC(),
	}, nil
}
