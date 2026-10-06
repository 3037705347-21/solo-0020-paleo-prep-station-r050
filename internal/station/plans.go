package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func toPlanParameters(request contracts.PlanParametersRequest) domain.PlanParameters {
	return domain.PlanParameters{
		Objective:      request.Objective,
		Method:         request.Method,
		Adhesive:       request.Adhesive,
		TargetExposure: request.TargetExposure,
		PeelStrength:   request.PeelStrength,
	}
}

func (s *Service) CreatePlan(
	ctx context.Context,
	specimenID string,
	request contracts.CreatePlanRequest,
) (domain.PrepPlan, error) {
	if err := ctx.Err(); err != nil {
		return domain.PrepPlan{}, err
	}

	parameters, err := domain.ValidatePlanParameters(toPlanParameters(request))
	if err != nil {
		return domain.PrepPlan{}, err
	}

	var created domain.PrepPlan
	err = s.repo.Mutate(func(m *repository.Mutation) error {
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
			return notFoundSpecimen(specimenID)
		}
		if m.HasOpenPlan(specimenID) {
			return contracts.Conflict("open_plan_exists", "该标本已存在未结束的修复方案")
		}
		// Resolve and enforce the restriction for the specimen's current
		// classification inside this locked mutation. The returned version is
		// snapshotted onto the plan and is never re-evaluated afterwards.
		policyVersion, err := evaluateAgainstCurrentPolicy(m, specimen, parameters)
		if err != nil {
			return err
		}
		plan, err := domain.NewPrepPlan(
			s.nextID("plan"),
			specimen,
			parameters,
			policyVersion,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := specimen.MarkPlanned(s.now()); err != nil {
			return err
		}
		m.PutPlan(plan)
		m.PutSpecimen(specimen)
		created = plan
		return nil
	})
	return created, err
}

// RevisePlan updates a draft plan's parameters. Only drafts are revisable and
// each revision is evaluated against the policy currently in force, but the
// snapshotted version on approved/completed plans stays untouched.
func (s *Service) RevisePlan(
	ctx context.Context,
	planID string,
	request contracts.RevisePlanRequest,
) (domain.PrepPlan, error) {
	if err := ctx.Err(); err != nil {
		return domain.PrepPlan{}, err
	}

	parameters, err := domain.ValidatePlanParameters(toPlanParameters(request))
	if err != nil {
		return domain.PrepPlan{}, err
	}

	var revised domain.PrepPlan
	err = s.repo.Mutate(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		specimen, ok := m.GetSpecimen(plan.SpecimenID)
		if !ok {
			return notFoundSpecimen(plan.SpecimenID)
		}
		policyVersion, err := evaluateAgainstCurrentPolicy(m, specimen, parameters)
		if err != nil {
			return err
		}
		if err := plan.Revise(parameters, policyVersion, s.now()); err != nil {
			return err
		}
		m.PutPlan(plan)
		revised = plan
		return nil
	})
	return revised, err
}

func (s *Service) ApprovePlan(
	ctx context.Context,
	planID string,
	request contracts.ApprovalRequest,
) (domain.PrepPlan, error) {
	if err := ctx.Err(); err != nil {
		return domain.PrepPlan{}, err
	}

	var approved domain.PrepPlan
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		specimen, ok := m.GetSpecimen(plan.SpecimenID)
		if !ok {
			return notFoundSpecimen(plan.SpecimenID)
		}
		if err := plan.Approve(request.ApprovedBy, s.now()); err != nil {
			return err
		}
		if err := specimen.MarkPreparing(s.now()); err != nil {
			return err
		}
		m.PutPlan(plan)
		m.PutSpecimen(specimen)
		approved = plan
		return nil
	})
	return approved, err
}

func (s *Service) GetPlan(ctx context.Context, planID string) (PlanSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return PlanSnapshot{}, err
	}
	var snapshot PlanSnapshot
	err := s.repo.Read(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		snapshot = PlanSnapshot{
			Plan:     plan,
			Progress: m.ProgressForPlan(planID),
		}
		return nil
	})
	return snapshot, err
}

func (s *Service) ListPlansForSpecimen(
	ctx context.Context,
	specimenID string,
) ([]domain.PrepPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var values []domain.PrepPlan
	err := s.repo.Read(func(m *repository.Mutation) error {
		if _, ok := m.GetSpecimen(specimenID); !ok {
			return notFoundSpecimen(specimenID)
		}
		values = m.ListPlansForSpecimen(specimenID)
		return nil
	})
	return values, err
}
