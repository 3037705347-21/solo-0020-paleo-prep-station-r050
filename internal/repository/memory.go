package repository

import (
	"sort"
	"sync"
	"time"

	"example.com/paleo-prep-station/internal/domain"
)

type state struct {
	specimens      map[string]domain.Specimen
	plans          map[string]domain.PrepPlan
	progress       map[string][]domain.ProgressEntry
	releases       map[string]domain.ReleaseRecord
	policyDraft    *domain.PolicyDraft
	policyVersions []domain.PolicyVersion
}

type MemoryRepository struct {
	mu    sync.Mutex
	state state
}

func NewMemory() *MemoryRepository {
	return &MemoryRepository{
		state: state{
			specimens:      make(map[string]domain.Specimen),
			plans:          make(map[string]domain.PrepPlan),
			progress:       make(map[string][]domain.ProgressEntry),
			releases:       make(map[string]domain.ReleaseRecord),
			policyVersions: make([]domain.PolicyVersion, 0),
		},
	}
}

func (r *MemoryRepository) Read(fn func(*Mutation) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fn(cloneMutation(r.state))
}

func (r *MemoryRepository) Mutate(fn func(*Mutation) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tx := cloneMutation(r.state)
	if err := fn(tx); err != nil {
		return err
	}
	r.state = tx.snapshot()
	return nil
}

func cloneMutation(source state) *Mutation {
	copied := state{
		specimens: make(map[string]domain.Specimen, len(source.specimens)),
		plans:     make(map[string]domain.PrepPlan, len(source.plans)),
		progress:  make(map[string][]domain.ProgressEntry, len(source.progress)),
		releases:  make(map[string]domain.ReleaseRecord, len(source.releases)),
	}
	for key, value := range source.specimens {
		copied.specimens[key] = value
	}
	for key, value := range source.plans {
		copied.plans[key] = value
	}
	for key, values := range source.progress {
		copied.progress[key] = append([]domain.ProgressEntry(nil), values...)
	}
	for key, value := range source.releases {
		copied.releases[key] = value
	}
	if source.policyDraft != nil {
		draft := &domain.PolicyDraft{
			Rules:     make(map[string]domain.RestrictionRule, len(source.policyDraft.Rules)),
			UpdatedAt: source.policyDraft.UpdatedAt,
		}
		for key, rule := range source.policyDraft.Rules {
			draft.Rules[key] = cloneRule(rule)
		}
		copied.policyDraft = draft
	}
	copied.policyVersions = make([]domain.PolicyVersion, len(source.policyVersions))
	for i, version := range source.policyVersions {
		copied.policyVersions[i] = cloneVersion(version)
	}
	return &Mutation{state: copied}
}

func cloneRule(rule domain.RestrictionRule) domain.RestrictionRule {
	rule.AllowedAdhesives = append([]string(nil), rule.AllowedAdhesives...)
	return rule
}

func cloneVersion(version domain.PolicyVersion) domain.PolicyVersion {
	cloned := make([]domain.RestrictionRule, len(version.Rules))
	for i, rule := range version.Rules {
		cloned[i] = cloneRule(rule)
	}
	version.Rules = cloned
	return version
}

type Mutation struct {
	state state
}

func (m *Mutation) snapshot() state {
	return m.state
}

func (m *Mutation) GetSpecimen(id string) (domain.Specimen, bool) {
	value, ok := m.state.specimens[id]
	return value, ok
}

func (m *Mutation) FindSpecimenByCatalog(catalogNumber string) (domain.Specimen, bool) {
	for _, value := range m.state.specimens {
		if value.CatalogNumber == catalogNumber {
			return value, true
		}
	}
	return domain.Specimen{}, false
}

func (m *Mutation) ListSpecimens() []domain.Specimen {
	values := make([]domain.Specimen, 0, len(m.state.specimens))
	for _, value := range m.state.specimens {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
	return values
}

func (m *Mutation) PutSpecimen(value domain.Specimen) {
	m.state.specimens[value.ID] = value
}

func (m *Mutation) GetPlan(id string) (domain.PrepPlan, bool) {
	value, ok := m.state.plans[id]
	return value, ok
}

func (m *Mutation) ListPlansForSpecimen(specimenID string) []domain.PrepPlan {
	values := make([]domain.PrepPlan, 0)
	for _, value := range m.state.plans {
		if value.SpecimenID == specimenID {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
	return values
}

func (m *Mutation) PutPlan(value domain.PrepPlan) {
	m.state.plans[value.ID] = value
}

func (m *Mutation) HasOpenPlan(specimenID string) bool {
	for _, value := range m.state.plans {
		if value.SpecimenID == specimenID && value.IsOpen() {
			return true
		}
	}
	return false
}

func (m *Mutation) ProgressForPlan(planID string) []domain.ProgressEntry {
	return append([]domain.ProgressEntry(nil), m.state.progress[planID]...)
}

func (m *Mutation) AppendProgress(value domain.ProgressEntry) {
	m.state.progress[value.PlanID] = append(m.state.progress[value.PlanID], value)
}

func (m *Mutation) ReleaseForSpecimen(specimenID string) (domain.ReleaseRecord, bool) {
	for _, value := range m.state.releases {
		if value.SpecimenID == specimenID {
			return value, true
		}
	}
	return domain.ReleaseRecord{}, false
}

func (m *Mutation) PutRelease(value domain.ReleaseRecord) {
	m.state.releases[value.ID] = value
}

// GetPolicyDraft returns the editable draft, materializing an empty one on
// first access so rule maintenance works before any publication.
func (m *Mutation) GetPolicyDraft(now time.Time) *domain.PolicyDraft {
	if m.state.policyDraft == nil {
		draft := domain.NewPolicyDraft(now)
		m.state.policyDraft = &draft
	}
	return m.state.policyDraft
}

func (m *Mutation) PolicyVersions() []domain.PolicyVersion {
	return m.state.policyVersions
}

// LatestPolicyVersion returns the current published version.
func (m *Mutation) LatestPolicyVersion() (domain.PolicyVersion, bool) {
	if len(m.state.policyVersions) == 0 {
		return domain.PolicyVersion{}, false
	}
	return m.state.policyVersions[len(m.state.policyVersions)-1], true
}

// PolicyVersionAt returns a specific published version by number.
func (m *Mutation) PolicyVersionAt(number int) (domain.PolicyVersion, bool) {
	for _, version := range m.state.policyVersions {
		if version.Number == number {
			return version, true
		}
	}
	return domain.PolicyVersion{}, false
}

// AppendPolicyVersion stores a freshly published snapshot. Published versions
// are append-only and never overwritten or removed.
func (m *Mutation) AppendPolicyVersion(version domain.PolicyVersion) {
	m.state.policyVersions = append(m.state.policyVersions, version)
}
