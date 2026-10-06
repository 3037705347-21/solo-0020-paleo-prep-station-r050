package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) RegisterSpecimen(
	ctx context.Context,
	request contracts.RegisterSpecimenRequest,
) (domain.Specimen, error) {
	if err := ctx.Err(); err != nil {
		return domain.Specimen{}, err
	}

	risk, err := domain.ParseRiskLevel(request.RiskLevel)
	if err != nil {
		return domain.Specimen{}, err
	}

	var created domain.Specimen
	err = s.repo.Mutate(func(m *repository.Mutation) error {
		value, err := domain.NewSpecimen(
			s.nextID("specimen"),
			request.CatalogNumber,
			request.FieldLabel,
			request.TaxonHint,
			request.MatrixKind,
			risk,
			request.JacketNotes,
			s.now(),
		)
		if err != nil {
			return err
		}
		if _, exists := m.FindSpecimenByCatalog(value.CatalogNumber); exists {
			return contracts.Conflict("duplicate_catalog_number", "目录号已存在，不能覆盖原标本")
		}
		m.PutSpecimen(value)
		created = value
		return nil
	})
	return created, err
}

// ReclassifySpecimen changes the matrix/risk classification that policy
// evaluation keys on. Reclassification is only allowed before a plan exists,
// so an open plan always keeps the classification it was evaluated against;
// past plans and progress are never rewritten.
func (s *Service) ReclassifySpecimen(
	ctx context.Context,
	specimenID string,
	request contracts.ReclassifySpecimenRequest,
) (domain.Specimen, error) {
	if err := ctx.Err(); err != nil {
		return domain.Specimen{}, err
	}
	risk, err := domain.ParseRiskLevel(request.RiskLevel)
	if err != nil {
		return domain.Specimen{}, err
	}

	var updated domain.Specimen
	err = s.repo.Mutate(func(m *repository.Mutation) error {
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
			return notFoundSpecimen(specimenID)
		}
		if m.HasOpenPlan(specimenID) {
			return contracts.Conflict(
				"open_plan_blocks_reclassification",
				"标本存在未结束的修复方案，分类不能再变更",
			)
		}
		if err := specimen.Reclassify(request.MatrixKind, risk, s.now()); err != nil {
			return err
		}
		m.PutSpecimen(specimen)
		updated = specimen
		return nil
	})
	return updated, err
}
