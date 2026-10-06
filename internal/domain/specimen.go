package domain

import (
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type SpecimenStatus string

const (
	SpecimenRegistered SpecimenStatus = "registered"
	SpecimenPlanned    SpecimenStatus = "planned"
	SpecimenPreparing  SpecimenStatus = "preparing"
	SpecimenStabilized SpecimenStatus = "stabilized"
	SpecimenAvailable  SpecimenStatus = "available"
)

type Specimen struct {
	ID            string         `json:"id"`
	CatalogNumber string         `json:"catalog_number"`
	FieldLabel    string         `json:"field_label"`
	TaxonHint     string         `json:"taxon_hint"`
	MatrixKind    string         `json:"matrix_kind"`
	RiskLevel     RiskLevel      `json:"risk_level"`
	JacketNotes   string         `json:"jacket_notes"`
	Status        SpecimenStatus `json:"status"`
	Version       int            `json:"version"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

func NewSpecimen(
	id string,
	catalogNumber string,
	fieldLabel string,
	taxonHint string,
	matrixKind string,
	riskLevel RiskLevel,
	jacketNotes string,
	now time.Time,
) (Specimen, error) {
	if id == "" {
		return Specimen{}, contracts.Internal("missing_generated_id", "标本标识未生成", nil)
	}
	catalog, err := validateCatalogNumber(catalogNumber)
	if err != nil {
		return Specimen{}, err
	}
	field, err := requiredText("野外标签", fieldLabel, 80)
	if err != nil {
		return Specimen{}, err
	}
	taxon, err := optionalText("分类提示", taxonHint, 120)
	if err != nil {
		return Specimen{}, err
	}
	matrix, err := requiredText("基质类型", matrixKind, 60)
	if err != nil {
		return Specimen{}, err
	}
	if riskLevel == "" {
		riskLevel = RiskLow
	}
	if riskLevel != RiskLow && riskLevel != RiskMedium && riskLevel != RiskHigh {
		return Specimen{}, contracts.Invalid("invalid_risk_level", "风险等级只能是 low、medium 或 high")
	}
	notes, err := optionalText("夹克备注", jacketNotes, 500)
	if err != nil {
		return Specimen{}, err
	}
	return Specimen{
		ID:            id,
		CatalogNumber: catalog,
		FieldLabel:    field,
		TaxonHint:     taxon,
		MatrixKind:    matrix,
		RiskLevel:     riskLevel,
		JacketNotes:   notes,
		Status:        SpecimenRegistered,
		Version:       1,
		CreatedAt:     now.UTC(),
		UpdatedAt:     now.UTC(),
	}, nil
}

func (s Specimen) CanCreatePlan() bool {
	return s.Status == SpecimenRegistered || s.Status == SpecimenPlanned
}

func (s Specimen) IsStable() bool {
	return s.Status == SpecimenStabilized
}

func (s *Specimen) MarkPlanned(now time.Time) error {
	if s.Status == SpecimenPlanned {
		return nil
	}
	if s.Status != SpecimenRegistered {
		return contracts.Conflict("specimen_not_plannable", "当前标本状态不能创建修复方案")
	}
	s.Status = SpecimenPlanned
	s.Version++
	s.UpdatedAt = now.UTC()
	return nil
}

func (s *Specimen) MarkPreparing(now time.Time) error {
	if s.Status == SpecimenPreparing {
		return nil
	}
	if s.Status != SpecimenPlanned {
		return contracts.Conflict("specimen_not_preparing", "只有已规划标本才能进入制备状态")
	}
	s.Status = SpecimenPreparing
	s.Version++
	s.UpdatedAt = now.UTC()
	return nil
}

func (s *Specimen) MarkStabilized(now time.Time) error {
	if s.Status == SpecimenStabilized {
		return nil
	}
	if s.Status != SpecimenPreparing {
		return contracts.Conflict("specimen_not_stabilizable", "只有正在制备的标本才能进入稳定状态")
	}
	s.Status = SpecimenStabilized
	s.Version++
	s.UpdatedAt = now.UTC()
	return nil
}

func (s *Specimen) MarkAvailable(now time.Time) error {
	if s.Status != SpecimenStabilized {
		return contracts.Conflict("specimen_not_releasable", "只有已稳定标本可以办理放行")
	}
	s.Status = SpecimenAvailable
	s.Version++
	s.UpdatedAt = now.UTC()
	return nil
}

// Reclassify updates the current classification used to resolve handling
// restrictions. An available specimen has left the preparation process and
// can never be reclassified; open-plan guards live in the service layer.
func (s *Specimen) Reclassify(matrixKind string, riskLevel RiskLevel, now time.Time) error {
	if s.Status == SpecimenAvailable {
		return contracts.Conflict("specimen_classification_locked", "标本已放行，分类不能再变更")
	}
	matrix, err := requiredText("基质类型", matrixKind, 60)
	if err != nil {
		return err
	}
	if riskLevel != RiskLow && riskLevel != RiskMedium && riskLevel != RiskHigh {
		return contracts.Invalid("invalid_risk_level", "风险等级只能是 low、medium 或 high")
	}
	if s.MatrixKind == matrix && s.RiskLevel == riskLevel {
		return nil
	}
	s.MatrixKind = matrix
	s.RiskLevel = riskLevel
	s.Version++
	s.UpdatedAt = now.UTC()
	return nil
}
