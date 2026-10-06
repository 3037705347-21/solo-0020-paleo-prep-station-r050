package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

// RiskLevel captures how fragile a specimen is given its jacket state and
// preservation. It is the second policy lookup key beside the matrix kind.
type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// ValidRiskLevel returns the normalized risk level or an error.
// An empty value is accepted at registration: the specimen is simply
// unclassified until the lead records its classification.
func ValidRiskLevel(value string) (RiskLevel, error) {
	cleaned := strings.ToLower(strings.TrimSpace(value))
	if cleaned == "" {
		return "", nil
	}
	switch RiskLevel(cleaned) {
	case RiskLow, RiskMedium, RiskHigh:
		return RiskLevel(cleaned), nil
	default:
		return "", contracts.Invalid("invalid_risk_level", "风险等级只能是 low、medium 或 high")
	}
}

// RestrictionRule is the operative policy entry for one matrix/risk pair.
type RestrictionRule struct {
	MatrixKind        string    `json:"matrix_kind"`
	RiskLevel         RiskLevel `json:"risk_level"`
	MaxPeelStrength   int       `json:"max_peel_strength"`
	AllowedAdhesives  []string  `json:"allowed_adhesives"`
	MaxTargetExposure int       `json:"max_target_exposure"`
	Notes             string    `json:"notes,omitempty"`
}

func (r RestrictionRule) key() string {
	return RuleKey(r.MatrixKind, r.RiskLevel)
}

// RuleKey is the stable lookup key shared by drafts and published versions.
func RuleKey(matrixKind string, risk RiskLevel) string {
	return strings.TrimSpace(matrixKind) + "\x00" + string(risk)
}

// NewRestrictionRule validates one restriction entry before it enters a draft.
func NewRestrictionRule(
	matrixKind string,
	riskLevel string,
	maxPeelStrength int,
	allowedAdhesives []string,
	maxTargetExposure int,
	notes string,
) (RestrictionRule, error) {
	matrix, err := requiredText("基质类型", matrixKind, 60)
	if err != nil {
		return RestrictionRule{}, err
	}
	risk, err := ValidRiskLevel(riskLevel)
	if err != nil {
		return RestrictionRule{}, err
	}
	if risk == "" {
		return RestrictionRule{}, contracts.Invalid("required_field", "风险等级不能为空")
	}
	if maxPeelStrength < 1 || maxPeelStrength > 100 {
		return RestrictionRule{}, contracts.Invalid("invalid_peel_strength", "最大可承受剥离强度必须位于 1 到 100 之间")
	}
	if maxTargetExposure < 1 || maxTargetExposure > 100 {
		return RestrictionRule{}, contracts.Invalid("invalid_target_exposure", "最大目标暴露比例必须位于 1 到 100 之间")
	}
	adhesives, err := normalizeAdhesives(allowedAdhesives)
	if err != nil {
		return RestrictionRule{}, err
	}
	noteValue, err := optionalText("限制说明", notes, 500)
	if err != nil {
		return RestrictionRule{}, err
	}
	return RestrictionRule{
		MatrixKind:        matrix,
		RiskLevel:         risk,
		MaxPeelStrength:   maxPeelStrength,
		AllowedAdhesives:  adhesives,
		MaxTargetExposure: maxTargetExposure,
		Notes:             noteValue,
	}, nil
}

func normalizeAdhesives(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		cleaned, err := requiredText("允许的黏结材料", value, 120)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[cleaned]; exists {
			return nil, contracts.Invalid("duplicate_adhesive", "允许的黏结材料存在重复："+cleaned)
		}
		seen[cleaned] = struct{}{}
		result = append(result, cleaned)
	}
	sort.Strings(result)
	return result, nil
}

// PolicyDraft holds the restriction entries the lead is currently maintaining.
// Publishing snapshots the draft into an immutable version.
type PolicyDraft struct {
	Rules     map[string]RestrictionRule
	UpdatedAt time.Time
}

func NewPolicyDraft(now time.Time) PolicyDraft {
	return PolicyDraft{
		Rules:     make(map[string]RestrictionRule),
		UpdatedAt: now.UTC(),
	}
}

// UpsertRule replaces the entry for one matrix/risk pair and returns the
// effective key so callers can echo the Location header.
func (d *PolicyDraft) UpsertRule(rule RestrictionRule, now time.Time) string {
	d.Rules[rule.key()] = rule
	d.UpdatedAt = now.UTC()
	return rule.key()
}

// RemoveRule deletes one draft entry. Missing entries are reported so the
// transport layer can answer 404 instead of silently doing nothing.
func (d *PolicyDraft) RemoveRule(matrixKind string, riskLevel string, now time.Time) bool {
	risk, err := ValidRiskLevel(riskLevel)
	if err != nil || risk == "" {
		return false
	}
	key := RuleKey(matrixKind, risk)
	if _, ok := d.Rules[key]; !ok {
		return false
	}
	delete(d.Rules, key)
	d.UpdatedAt = now.UTC()
	return true
}

// SortedRules exposes entries in a deterministic order for views and hashing.
func (d PolicyDraft) SortedRules() []RestrictionRule {
	return sortedRules(d.Rules)
}

// PolicyVersion is an immutable published snapshot. Once created it is never
// rewritten, so plans stamped with it keep evaluating against fixed limits.
type PolicyVersion struct {
	Number      int               `json:"number"`
	Rules       []RestrictionRule `json:"rules"`
	ContentHash string            `json:"content_hash"`
	PublishedBy string            `json:"published_by"`
	PublishedAt time.Time         `json:"published_at"`
}

// FindRule returns the restriction for a matrix/risk classification.
func (v PolicyVersion) FindRule(matrixKind string, risk RiskLevel) (RestrictionRule, bool) {
	for _, rule := range v.Rules {
		if rule.key() == RuleKey(matrixKind, risk) {
			return rule, true
		}
	}
	return RestrictionRule{}, false
}

// PublishPolicy snapshots the draft as the next version. Publishing the exact
// same content as the latest version is rejected as a duplicate so that
// double-clicks and racing retries have one deterministic answer.
func PublishPolicy(
	draft PolicyDraft,
	versions []PolicyVersion,
	publishedBy string,
	now time.Time,
) (PolicyVersion, error) {
	leader, err := requiredText("发布人", publishedBy, 80)
	if err != nil {
		return PolicyVersion{}, err
	}
	if len(draft.Rules) == 0 {
		return PolicyVersion{}, contracts.Invalid("empty_policy", "政策草稿没有任何限制规则，不能发布")
	}
	rules := draft.SortedRules()
	hash := hashRules(rules)
	if len(versions) > 0 && versions[len(versions)-1].ContentHash == hash {
		return PolicyVersion{}, contracts.Conflict("policy_unchanged", "最新版本与草稿内容相同，不能重复发布")
	}
	number := 1
	if len(versions) > 0 {
		number = versions[len(versions)-1].Number + 1
	}
	return PolicyVersion{
		Number:      number,
		Rules:       rules,
		ContentHash: hash,
		PublishedBy: leader,
		PublishedAt: now.UTC(),
	}, nil
}

func sortedRules(rules map[string]RestrictionRule) []RestrictionRule {
	values := make([]RestrictionRule, 0, len(rules))
	for _, rule := range rules {
		values = append(values, rule)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].MatrixKind != values[j].MatrixKind {
			return values[i].MatrixKind < values[j].MatrixKind
		}
		return string(values[i].RiskLevel) < string(values[j].RiskLevel)
	})
	return values
}

// hashRules is order-independent only over the map content: rules are always
// sorted first, so identical drafts always produce the identical hash.
func hashRules(rules []RestrictionRule) string {
	hasher := sha256.New()
	for _, rule := range rules {
		line := rule.MatrixKind + "\x00" +
			string(rule.RiskLevel) + "\x00" +
			strconv.Itoa(rule.MaxPeelStrength) + "\x00" +
			strings.Join(rule.AllowedAdhesives, ",") + "\x00" +
			strconv.Itoa(rule.MaxTargetExposure) + "\n"
		hasher.Write([]byte(line))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// PlanRestriction carries the plan parameters that policy limits apply to.
type PlanRestriction struct {
	PeelStrength   int
	Adhesive       string
	TargetExposure int
}

// EvaluateRestriction checks a plan against the rule governing the specimen's
// current classification. Field-level violations are returned in Details so
// the field team sees every crossed boundary at once.
func EvaluateRestriction(rule RestrictionRule, params PlanRestriction) error {
	details := map[string]string{}
	if params.PeelStrength < 1 || params.PeelStrength > 100 {
		details["peel_strength"] = "剥离强度必须位于 1 到 100 之间"
	} else if params.PeelStrength > rule.MaxPeelStrength {
		details["peel_strength"] = "剥离强度超出该基质与风险等级允许的上限"
	}
	adhesive := strings.TrimSpace(params.Adhesive)
	if adhesive != "" {
		allowed := false
		for _, candidate := range rule.AllowedAdhesives {
			if candidate == adhesive {
				allowed = true
				break
			}
		}
		if !allowed {
			details["adhesive"] = "黏结材料不在该基质与风险等级的允许清单内"
		}
	}
	if params.TargetExposure > rule.MaxTargetExposure {
		details["target_exposure"] = "目标暴露比例超出该基质与风险等级允许的上限"
	}
	if len(details) > 0 {
		return contracts.ConflictDetails("restriction_violation", "修复参数超出标本处置限制政策", details)
	}
	return nil
}
