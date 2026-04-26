package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AttestationExportService struct {
	attestationService *AttestationService
}

type complianceBundle struct {
	GeneratedAt   string              `json:"generated_at"`
	OrgID         string              `json:"org_id"`
	FilterSummary complianceFilters   `json:"filters"`
	Records       []AttestationRecord `json:"records"`
}

type complianceFilters struct {
	RepoID      string `json:"repo_id,omitempty"`
	StartDate   string `json:"start_date,omitempty"`
	EndDate     string `json:"end_date,omitempty"`
	ExecutionID string `json:"execution_id,omitempty"`
}

func NewAttestationExportService(attestationService *AttestationService) (*AttestationExportService, error) {
	if attestationService == nil {
		return nil, fmt.Errorf("attestation service must not be nil")
	}
	return &AttestationExportService{attestationService: attestationService}, nil
}

func (s *AttestationExportService) ExportComplianceBundle(ctx context.Context, orgID uuid.UUID, filters AttestationFilters) ([]byte, error) {
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("org id must not be empty")
	}
	if filters.Page == 0 {
		filters.Page = 1
	}
	filters.PageSize = 50

	records, err := s.attestationService.ListAttestationsForOrg(ctx, orgID.String(), filters)
	if err != nil {
		return nil, err
	}

	bundle := complianceBundle{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		OrgID:       orgID.String(),
		FilterSummary: complianceFilters{
			RepoID:      strings.TrimSpace(filters.RepoID),
			ExecutionID: strings.TrimSpace(filters.ExecutionID),
		},
		Records: records,
	}
	if filters.StartDate != nil {
		bundle.FilterSummary.StartDate = filters.StartDate.UTC().Format(time.RFC3339Nano)
	}
	if filters.EndDate != nil {
		bundle.FilterSummary.EndDate = filters.EndDate.UTC().Format(time.RFC3339Nano)
	}

	encoded, err := json.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("marshal compliance bundle: %w", err)
	}
	return encoded, nil
}
