package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/httpapi"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/station"
)

type specimenView struct {
	ID            string `json:"id"`
	CatalogNumber string `json:"catalog_number"`
	MatrixKind    string `json:"matrix_kind"`
	RiskLevel     string `json:"risk_level"`
	Status        string `json:"status"`
}

type planView struct {
	ID               string `json:"id"`
	SpecimenID       string `json:"specimen_id"`
	Status           string `json:"status"`
	PeelStrength     int    `json:"peel_strength"`
	TargetExposure   int    `json:"target_exposure"`
	PolicyVersion    int    `json:"policy_version"`
	PolicyMatrixKind string `json:"policy_matrix_kind"`
	PolicyRiskLevel  string `json:"policy_risk_level"`
}

type planSnapshot struct {
	Plan     planView `json:"plan"`
	Progress []struct {
		Percent int `json:"percent"`
	} `json:"progress"`
}

type releaseView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
}

type errorView struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details"`
}

type policyVersionView struct {
	Number int `json:"number"`
	Rules  []struct {
		MatrixKind       string   `json:"matrix_kind"`
		RiskLevel        string   `json:"risk_level"`
		MaxPeelStrength  int      `json:"max_peel_strength"`
		AllowedAdhesives []string `json:"allowed_adhesives"`
	} `json:"rules"`
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to check")
	flag.Parse()

	if err := run(*workflow); err != nil {
		fmt.Fprintln(os.Stderr, "workflow check failed:", err)
		os.Exit(1)
	}
	fmt.Printf("workflow check passed: %s\n", *workflow)
}

func run(workflow string) error {
	handler := httpapi.NewRouter(station.New(repository.NewMemory()))
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	switch workflow {
	case "register-specimen":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		if specimen.Status != "registered" {
			return fmt.Errorf("expected registered status, got %s", specimen.Status)
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.ID != specimen.ID {
			return errors.New("registered specimen cannot be read back")
		}
		return nil

	case "approve-prep-plan":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		if err := seedBaselinePolicy(client, server.URL); err != nil {
			return err
		}
		plan, err := createPlan(client, server.URL, specimen.ID)
		if err != nil {
			return err
		}
		if plan.PolicyVersion != 1 {
			return fmt.Errorf("expected plan stamped with policy version 1, got %d", plan.PolicyVersion)
		}
		var approved planView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/plans/"+plan.ID+"/approval",
			contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
			http.StatusOK,
			&approved,
		); err != nil {
			return err
		}
		if approved.Status != "approved" {
			return fmt.Errorf("expected approved plan, got %s", approved.Status)
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "preparing" {
			return fmt.Errorf("expected preparing specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "log-preparation":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 35, "矩阵剥离"); err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var snapshot planSnapshot
		if err := requestJSON(client, http.MethodGet, server.URL+"/plans/"+plan.ID, nil, http.StatusOK, &snapshot); err != nil {
			return err
		}
		if snapshot.Plan.Status != "completed" || len(snapshot.Progress) != 2 {
			return errors.New("plan did not complete with exactly two progress entries")
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "stabilized" {
			return fmt.Errorf("expected stabilized specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "release-for-study":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var release releaseView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/specimens/"+specimen.ID+"/release",
			contracts.ReleaseRequest{
				Purpose:    "比较解剖研究",
				Recipient:  "地层研究组",
				ApprovedBy: "keeper-zhou",
			},
			http.StatusCreated,
			&release,
		); err != nil {
			return err
		}
		if release.SpecimenID != specimen.ID {
			return errors.New("release record references the wrong specimen")
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.Status != "available" {
			return fmt.Errorf("expected available specimen, got %s", fetched.Status)
		}
		return nil

	case "restriction-policy":
		return checkRestrictionPolicy(client, server.URL)
	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

// checkRestrictionPolicy exercises the full restriction policy capability:
// policy maintenance and publishing, plan gating on the current classification,
// non-retroactive versions, revision, and deterministic concurrency outcomes.
func checkRestrictionPolicy(client *http.Client, baseURL string) error {
	// No version has ever been published: plan creation is refused rather
	// than silently running without limits.
	noPolicySpecimen, err := registerSpecimenAs(client, baseURL, "PPS-POL-001", "粉砂岩", "low")
	if err != nil {
		return err
	}
	if err := expectPlanError(client, baseURL, noPolicySpecimen.ID, 40, 80, "可逆树脂",
		http.StatusConflict, "restriction_policy_unpublished"); err != nil {
		return err
	}

	// An unclassified specimen gets a distinct deterministic answer.
	unclassified, err := registerSpecimenAs(client, baseURL, "PPS-POL-002", "粉砂岩", "")
	if err != nil {
		return err
	}
	if err := expectPlanError(client, baseURL, unclassified.ID, 40, 80, "可逆树脂",
		http.StatusConflict, "specimen_unclassified"); err != nil {
		return err
	}

	// Lead maintains v1: 粉砂岩/low allows peel 50, one adhesive, exposure 90.
	if err := putPolicyRule(client, baseURL, contracts.RestrictionRuleRequest{
		MatrixKind:        "粉砂岩",
		RiskLevel:         "low",
		MaxPeelStrength:   50,
		AllowedAdhesives:  []string{"可逆树脂"},
		MaxTargetExposure: 90,
	}); err != nil {
		return err
	}
	if err := publishPolicyExpect(client, baseURL, http.StatusCreated, 1); err != nil {
		return err
	}
	// Duplicate publication of identical content: deterministic 409.
	if err := publishPolicyExpect(client, baseURL, http.StatusConflict, 0); err != nil {
		return err
	}

	// High-risk 粉砂岩 has no rule yet.
	highSpecimen, err := registerSpecimenAs(client, baseURL, "PPS-POL-003", "粉砂岩", "high")
	if err != nil {
		return err
	}
	if err := expectPlanError(client, baseURL, highSpecimen.ID, 20, 60, "可逆树脂",
		http.StatusConflict, "restriction_rule_missing"); err != nil {
		return err
	}

	// Compliant plan on 001 is stamped with v1 and its classification.
	planA, err := createPlanFull(client, baseURL, noPolicySpecimen.ID, 40, 80, "可逆树脂")
	if err != nil {
		return err
	}
	if planA.PolicyVersion != 1 || planA.PolicyMatrixKind != "粉砂岩" || planA.PolicyRiskLevel != "low" {
		return fmt.Errorf("plan stamp mismatch: version=%d matrix=%q risk=%q",
			planA.PolicyVersion, planA.PolicyMatrixKind, planA.PolicyRiskLevel)
	}

	// Every crossed boundary is reported with field-level details.
	boundarySpecimen, err := registerSpecimenAs(client, baseURL, "PPS-POL-004", "粉砂岩", "low")
	if err != nil {
		return err
	}
	if err := expectViolation(client, baseURL, boundarySpecimen.ID, 60, 80, "可逆树脂", "peel_strength"); err != nil {
		return err
	}
	if err := expectViolation(client, baseURL, boundarySpecimen.ID, 40, 95, "可逆树脂", "target_exposure"); err != nil {
		return err
	}
	if err := expectViolation(client, baseURL, boundarySpecimen.ID, 40, 80, "氰基丙烯酸酯", "adhesive"); err != nil {
		return err
	}

	// Tighten the limits and publish v2. The v1-stamped plan must keep
	// moving: approval and progress never re-evaluate policy.
	if err := putPolicyRule(client, baseURL, contracts.RestrictionRuleRequest{
		MatrixKind:        "粉砂岩",
		RiskLevel:         "low",
		MaxPeelStrength:   30,
		AllowedAdhesives:  []string{"可逆树脂"},
		MaxTargetExposure: 70,
	}); err != nil {
		return err
	}
	if err := publishPolicyExpect(client, baseURL, http.StatusCreated, 2); err != nil {
		return err
	}
	var approved planView
	if err := requestJSON(client, http.MethodPost, baseURL+"/plans/"+planA.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"}, http.StatusOK, &approved); err != nil {
		return err
	}
	if err := recordProgress(client, baseURL, planA.ID, 50, "矩阵剥离"); err != nil {
		return err
	}
	var snapshot planSnapshot
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+planA.ID, nil, http.StatusOK, &snapshot); err != nil {
		return err
	}
	if snapshot.Plan.PolicyVersion != 1 {
		return fmt.Errorf("policy update rewrote historical plan stamp: %d", snapshot.Plan.PolicyVersion)
	}

	// A fresh draft is gated by v2: revise into the allowed range succeeds
	// and re-stamps; revising an approved plan is rejected.
	revisionSpecimen, err := registerSpecimenAs(client, baseURL, "PPS-POL-005", "粉砂岩", "low")
	if err != nil {
		return err
	}
	planB, err := createPlanFull(client, baseURL, revisionSpecimen.ID, 30, 70, "可逆树脂")
	if err != nil {
		return err
	}
	if planB.PolicyVersion != 2 {
		return fmt.Errorf("expected new plan stamped with v2, got %d", planB.PolicyVersion)
	}
	if err := revisePlanExpect(client, baseURL, planB.ID, 40, 70, "可逆树脂",
		http.StatusConflict, "restriction_violation"); err != nil {
		return err
	}
	var revised planView
	if err := revisePlan(client, baseURL, planB.ID, 20, 60, "可逆树脂", http.StatusOK, &revised); err != nil {
		return err
	}
	if revised.PeelStrength != 20 || revised.PolicyVersion != 2 {
		return fmt.Errorf("revision not applied or restamped: peel=%d version=%d",
			revised.PeelStrength, revised.PolicyVersion)
	}
	if err := requestJSON(client, http.MethodPost, baseURL+"/plans/"+planB.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"}, http.StatusOK, &approved); err != nil {
		return err
	}
	if err := revisePlanExpect(client, baseURL, planB.ID, 10, 50, "可逆树脂",
		http.StatusConflict, "plan_not_editable"); err != nil {
		return err
	}

	// Reclassification changes current lookup; adding the missing rule to a
	// new version unblocks the specimen.
	reclassified, err := registerSpecimenAs(client, baseURL, "PPS-POL-006", "粉砂岩", "low")
	if err != nil {
		return err
	}
	var current specimenView
	if err := requestJSON(client, http.MethodPost, baseURL+"/specimens/"+reclassified.ID+"/classification",
		contracts.ClassificationRequest{MatrixKind: "板岩", RiskLevel: "high"}, http.StatusOK, &current); err != nil {
		return err
	}
	if current.MatrixKind != "板岩" || current.RiskLevel != "high" {
		return fmt.Errorf("reclassification not applied: matrix=%q risk=%q", current.MatrixKind, current.RiskLevel)
	}
	if err := expectPlanError(client, baseURL, reclassified.ID, 20, 60, "可逆树脂",
		http.StatusConflict, "restriction_rule_missing"); err != nil {
		return err
	}
	if err := putPolicyRule(client, baseURL, contracts.RestrictionRuleRequest{
		MatrixKind:        "板岩",
		RiskLevel:         "high",
		MaxPeelStrength:   20,
		AllowedAdhesives:  []string{"可逆树脂"},
		MaxTargetExposure: 60,
	}); err != nil {
		return err
	}
	if err := publishPolicyExpect(client, baseURL, http.StatusCreated, 3); err != nil {
		return err
	}
	planC, err := createPlanFull(client, baseURL, reclassified.ID, 20, 60, "可逆树脂")
	if err != nil {
		return err
	}
	if planC.PolicyVersion != 3 {
		return fmt.Errorf("expected v3 stamp, got %d", planC.PolicyVersion)
	}

	// Removing a draft entry never rewrites a published version.
	status, _, err := doJSON(client, http.MethodDelete, baseURL+"/policy/rules/板岩/high", nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return fmt.Errorf("delete draft rule returned %d, want 204", status)
	}
	var version3 policyVersionView
	if err := requestJSON(client, http.MethodGet, baseURL+"/policy/versions/3", nil, http.StatusOK, &version3); err != nil {
		return err
	}
	if len(version3.Rules) != 2 {
		return fmt.Errorf("draft removal rewrote published v3: %d rules remain", len(version3.Rules))
	}

	// The effective restriction view resolves against the latest version.
	type restrictionView struct {
		PolicyVersion int `json:"policy_version"`
		Rule          struct {
			MaxPeelStrength int `json:"max_peel_strength"`
		} `json:"rule"`
	}
	var effective restrictionView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+noPolicySpecimen.ID+"/restriction",
		nil, http.StatusOK, &effective); err != nil {
		return err
	}
	if effective.PolicyVersion != 3 || effective.Rule.MaxPeelStrength != 30 {
		return fmt.Errorf("effective restriction mismatch: version=%d peel=%d",
			effective.PolicyVersion, effective.Rule.MaxPeelStrength)
	}

	if err := checkConcurrentPublish(client, baseURL); err != nil {
		return err
	}
	if err := checkConcurrentPlanCreation(client, baseURL); err != nil {
		return err
	}
	return nil
}

// checkConcurrentPublish fires identical publishes: under the repository
// mutex exactly one wins; every racing duplicate gets policy_unchanged.
func checkConcurrentPublish(client *http.Client, baseURL string) error {
	if err := putPolicyRule(client, baseURL, contracts.RestrictionRuleRequest{
		MatrixKind:        "灰岩",
		RiskLevel:         "medium",
		MaxPeelStrength:   70,
		AllowedAdhesives:  []string{"可逆树脂"},
		MaxTargetExposure: 90,
	}); err != nil {
		return err
	}
	const attempts = 10
	var wg sync.WaitGroup
	results := make([]int, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			status, _, err := doJSON(client, http.MethodPost, baseURL+"/policy/publications",
				map[string]string{"published_by": "curator-lin"})
			if err != nil {
				results[index] = 0
				return
			}
			results[index] = status
		}(i)
	}
	wg.Wait()
	created, duplicated := 0, 0
	for _, status := range results {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			duplicated++
		default:
			return fmt.Errorf("concurrent publish returned unexpected status %d", status)
		}
	}
	if created != 1 || duplicated != attempts-1 {
		return fmt.Errorf("concurrent publish outcome not deterministic: %d created, %d duplicated", created, duplicated)
	}
	var list struct {
		Items []policyVersionView `json:"items"`
	}
	if err := requestJSON(client, http.MethodGet, baseURL+"/policy/versions", nil, http.StatusOK, &list); err != nil {
		return err
	}
	if len(list.Items) != 4 {
		return fmt.Errorf("expected 4 versions after concurrent publish, got %d", len(list.Items))
	}
	return nil
}

// checkConcurrentPlanCreation serializes evaluation inside one mutex: every
// racing compliant plan must commit, none lost or double-rejected.
func checkConcurrentPlanCreation(client *http.Client, baseURL string) error {
	const count = 12
	specimens := make([]string, count)
	for i := range specimens {
		item, err := registerSpecimenAs(client, baseURL,
			fmt.Sprintf("PPS-CC-%02d", i+1), "粉砂岩", "low")
		if err != nil {
			return err
		}
		specimens[i] = item.ID
	}
	var wg sync.WaitGroup
	results := make([]int, count)
	for i, specimenID := range specimens {
		wg.Add(1)
		go func(index int, id string) {
			defer wg.Done()
			status, _, err := doJSON(client, http.MethodPost, baseURL+"/specimens/"+id+"/plans",
				contracts.CreatePlanRequest{
					Objective:      "暴露主要关节面",
					Method:         "手工显微剥离",
					Adhesive:       "可逆树脂",
					PeelStrength:   20,
					TargetExposure: 60,
				})
			if err != nil {
				results[index] = 0
				return
			}
			results[index] = status
		}(i, specimenID)
	}
	wg.Wait()
	for i, status := range results {
		if status != http.StatusCreated {
			return fmt.Errorf("concurrent plan %d got status %d, want 201", i+1, status)
		}
	}
	for _, specimenID := range specimens {
		var list struct {
			Items []planView `json:"items"`
		}
		if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimenID+"/plans",
			nil, http.StatusOK, &list); err != nil {
			return err
		}
		if len(list.Items) != 1 {
			return fmt.Errorf("specimen %s ended with %d plans, want exactly 1", specimenID, len(list.Items))
		}
	}
	return nil
}

func seedBaselinePolicy(client *http.Client, baseURL string) error {
	if err := putPolicyRule(client, baseURL, contracts.RestrictionRuleRequest{
		MatrixKind:        "粉砂岩",
		RiskLevel:         "low",
		MaxPeelStrength:   50,
		AllowedAdhesives:  []string{"可逆树脂"},
		MaxTargetExposure: 90,
	}); err != nil {
		return err
	}
	return publishPolicyExpect(client, baseURL, http.StatusCreated, 1)
}

func putPolicyRule(client *http.Client, baseURL string, rule contracts.RestrictionRuleRequest) error {
	var saved struct {
		MatrixKind string `json:"matrix_kind"`
		RiskLevel  string `json:"risk_level"`
	}
	return requestJSON(client, http.MethodPost, baseURL+"/policy/rules", rule, http.StatusOK, &saved)
}

func publishPolicyExpect(client *http.Client, baseURL string, wantStatus, wantNumber int) error {
	status, body, err := doJSON(client, http.MethodPost, baseURL+"/policy/publications",
		map[string]string{"published_by": "curator-lin"})
	if err != nil {
		return err
	}
	if status != wantStatus {
		return fmt.Errorf("publish returned %d (want %d): %s", status, wantStatus, string(body))
	}
	if wantStatus == http.StatusCreated {
		var version policyVersionView
		if err := json.Unmarshal(body, &version); err != nil {
			return err
		}
		if version.Number != wantNumber {
			return fmt.Errorf("published version number %d, want %d", version.Number, wantNumber)
		}
	}
	return nil
}

func expectPlanError(
	client *http.Client,
	baseURL, specimenID string,
	peel, exposure int,
	adhesive string,
	wantStatus int,
	wantCode string,
) error {
	return expectPlanErrorShape(client, baseURL, specimenID, peel, exposure, adhesive, wantStatus, wantCode, "")
}

func expectViolation(
	client *http.Client,
	baseURL, specimenID string,
	peel, exposure int,
	adhesive string,
	detailField string,
) error {
	return expectPlanErrorShape(client, baseURL, specimenID, peel, exposure, adhesive,
		http.StatusConflict, "restriction_violation", detailField)
}

func expectPlanErrorShape(
	client *http.Client,
	baseURL, specimenID string,
	peel, exposure int,
	adhesive string,
	wantStatus int,
	wantCode, detailField string,
) error {
	status, body, err := doJSON(client, http.MethodPost, baseURL+"/specimens/"+specimenID+"/plans",
		planRequestBody(peel, exposure, adhesive))
	if err != nil {
		return err
	}
	if status != wantStatus {
		return fmt.Errorf("plan creation returned %d (want %d): %s", status, wantStatus, string(body))
	}
	var problem errorView
	if err := json.Unmarshal(body, &problem); err != nil {
		return err
	}
	if problem.Code != wantCode {
		return fmt.Errorf("error code %q, want %q", problem.Code, wantCode)
	}
	if detailField != "" {
		if _, ok := problem.Details[detailField]; !ok {
			return fmt.Errorf("violation details %q missing: %v", detailField, problem.Details)
		}
	}
	return nil
}

func revisePlanExpect(
	client *http.Client,
	baseURL, planID string,
	peel, exposure int,
	adhesive string,
	wantStatus int,
	wantCode string,
) error {
	status, body, err := doJSON(client, http.MethodPut, baseURL+"/plans/"+planID,
		planRequestBody(peel, exposure, adhesive))
	if err != nil {
		return err
	}
	if status != wantStatus {
		return fmt.Errorf("revise returned %d (want %d): %s", status, wantStatus, string(body))
	}
	if wantStatus != http.StatusOK {
		var problem errorView
		if err := json.Unmarshal(body, &problem); err != nil {
			return err
		}
		if problem.Code != wantCode {
			return fmt.Errorf("revise error code %q, want %q", problem.Code, wantCode)
		}
	}
	return nil
}

func revisePlan(
	client *http.Client,
	baseURL, planID string,
	peel, exposure int,
	adhesive string,
	wantStatus int,
	target any,
) error {
	return requestJSON(client, http.MethodPut, baseURL+"/plans/"+planID,
		planRequestBody(peel, exposure, adhesive), wantStatus, target)
}

func planRequestBody(peel, exposure int, adhesive string) contracts.CreatePlanRequest {
	return contracts.CreatePlanRequest{
		Objective:      "暴露主要关节面",
		Method:         "手工显微剥离",
		Adhesive:       adhesive,
		PeelStrength:   peel,
		TargetExposure: exposure,
	}
}

func registerSpecimen(client *http.Client, baseURL string) (specimenView, error) {
	return registerSpecimenAs(client, baseURL, "PPS-2026-001", "粉砂岩", "low")
}

func registerSpecimenAs(
	client *http.Client,
	baseURL, catalog, matrix, risk string,
) (specimenView, error) {
	var specimen specimenView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: catalog,
			FieldLabel:    "北坡-07",
			TaxonHint:     "大型脊椎动物",
			MatrixKind:    matrix,
			RiskLevel:     risk,
			JacketNotes:   "夹克边角完好",
		},
		http.StatusCreated,
		&specimen,
	)
	return specimen, err
}

func createPlan(client *http.Client, baseURL, specimenID string) (planView, error) {
	return createPlanFull(client, baseURL, specimenID, 40, 80, "可逆树脂")
}

func createPlanFull(
	client *http.Client,
	baseURL, specimenID string,
	peel, exposure int,
	adhesive string,
) (planView, error) {
	var plan planView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimenID+"/plans",
		planRequestBody(peel, exposure, adhesive),
		http.StatusCreated,
		&plan,
	)
	return plan, err
}

func completedPlanSetup(client *http.Client, baseURL string) (specimenView, planView, error) {
	specimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	if err := seedBaselinePolicy(client, baseURL); err != nil {
		return specimenView{}, planView{}, err
	}
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	var approved planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approved,
	); err != nil {
		return specimenView{}, planView{}, err
	}
	return specimen, approved, nil
}

func recordProgress(client *http.Client, baseURL, planID string, percent int, stage string) error {
	var ignored struct{}
	return requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+planID+"/progress",
		contracts.ProgressRequest{
			Stage:      stage,
			Percent:    percent,
			Note:       "本地链路检查",
			RecordedBy: "preparator-chen",
		},
		http.StatusCreated,
		&ignored,
	)
}

func doJSON(client *http.Client, method, url string, body any) (int, []byte, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, raw, nil
}

func requestJSON(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
	target any,
) error {
	status, raw, err := doJSON(client, method, url, body)
	if err != nil {
		return err
	}
	if status != wantStatus {
		return fmt.Errorf("request %s %s returned %d: %s", method, url, status, string(raw))
	}
	if target == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode response for %s %s: %w", method, url, err)
	}
	return nil
}
