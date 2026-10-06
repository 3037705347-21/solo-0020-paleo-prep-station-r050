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
	"sort"
	"strconv"
	"strings"
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
	ID            string `json:"id"`
	SpecimenID    string `json:"specimen_id"`
	Status        string `json:"status"`
	PolicyVersion int    `json:"policy_version"`
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

type draftView struct {
	Revision int `json:"revision"`
}

type policyView struct {
	Version int `json:"version"`
}

type errorView struct {
	Code string `json:"code"`
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
		if specimen.RiskLevel != "low" {
			return fmt.Errorf("expected default low risk level, got %s", specimen.RiskLevel)
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
		if _, err := publishStandardPolicy(client, server.URL); err != nil {
			return err
		}
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		plan, err := createPlan(client, server.URL, specimen.ID)
		if err != nil {
			return err
		}
		if plan.PolicyVersion != 1 {
			return fmt.Errorf("expected policy snapshot version 1, got %d", plan.PolicyVersion)
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
		if _, err := publishStandardPolicy(client, server.URL); err != nil {
			return err
		}
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
		if _, err := publishStandardPolicy(client, server.URL); err != nil {
			return err
		}
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

	case "publish-handling-policy":
		return checkPublishHandlingPolicy(client, server.URL)

	case "enforce-handling-policy":
		return checkEnforceHandlingPolicy(client, server.URL)

	case "concurrent-policy-evaluation":
		return checkConcurrentPolicyEvaluation(client, server.URL)

	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

func checkPublishHandlingPolicy(client *http.Client, baseURL string) error {
	// A duplicated matrix/risk row cannot be saved.
	dupRules := []contracts.HandlingRuleRequest{standardRule(), standardRule()}
	if err := expectError(
		client, http.MethodPut, baseURL+"/policies/handling/draft",
		contracts.SavePolicyDraftRequest{UpdatedBy: "lead-he", Rules: dupRules},
		http.StatusConflict, "duplicate_rule",
	); err != nil {
		return err
	}

	draft := contracts.SavePolicyDraftRequest{UpdatedBy: "lead-he", Rules: []contracts.HandlingRuleRequest{standardRule()}}
	var saved draftView
	if err := requestJSON(client, http.MethodPut, baseURL+"/policies/handling/draft", draft, http.StatusOK, &saved); err != nil {
		return err
	}
	if saved.Revision != 1 {
		return fmt.Errorf("expected first draft revision 1, got %d", saved.Revision)
	}

	// A stale optimistic revision must be rejected deterministically.
	stale := draft
	stale.ExpectedRevision = 99
	if err := expectError(
		client, http.MethodPut, baseURL+"/policies/handling/draft", stale,
		http.StatusConflict, "policy_draft_revision_conflict",
	); err != nil {
		return err
	}
	// The matching expected revision succeeds and bumps the revision.
	current := draft
	current.ExpectedRevision = 1
	if err := requestJSON(client, http.MethodPut, baseURL+"/policies/handling/draft", current, http.StatusOK, &saved); err != nil {
		return err
	}
	if saved.Revision != 2 {
		return fmt.Errorf("expected draft revision 2, got %d", saved.Revision)
	}

	var version1 policyView
	if err := requestJSON(
		client, http.MethodPost, baseURL+"/policies/handling/publish",
		contracts.PublishPolicyRequest{PublishedBy: "lead-he"},
		http.StatusCreated, &version1,
	); err != nil {
		return err
	}
	if version1.Version != 1 {
		return fmt.Errorf("expected published version 1, got %d", version1.Version)
	}

	// Publishing consumes the draft; a repeat publish cannot create a second v1.
	if err := expectError(
		client, http.MethodPost, baseURL+"/policies/handling/publish",
		contracts.PublishPolicyRequest{PublishedBy: "lead-he"},
		http.StatusConflict, "no_active_policy_draft",
	); err != nil {
		return err
	}

	// Editing a new draft and republishing yields the next monotonic version.
	draft.Rules[0].MaxTargetExposure = 90
	if err := requestJSON(client, http.MethodPut, baseURL+"/policies/handling/draft", draft, http.StatusOK, &saved); err != nil {
		return err
	}
	if saved.Revision != 1 {
		return fmt.Errorf("expected new draft to restart at revision 1, got %d", saved.Revision)
	}
	var version2 policyView
	if err := requestJSON(
		client, http.MethodPost, baseURL+"/policies/handling/publish",
		contracts.PublishPolicyRequest{PublishedBy: "lead-he"},
		http.StatusCreated, &version2,
	); err != nil {
		return err
	}
	if version2.Version != 2 {
		return fmt.Errorf("expected published version 2, got %d", version2.Version)
	}

	// Old versions stay readable and immutable.
	var fetched policyView
	if err := requestJSON(client, http.MethodGet, baseURL+"/policies/handling/versions/1", nil, http.StatusOK, &fetched); err != nil {
		return err
	}
	if fetched.Version != 1 {
		return errors.New("published policy version 1 cannot be read back")
	}
	if err := expectError(
		client, http.MethodGet, baseURL+"/policies/handling/versions/99", nil,
		http.StatusNotFound, "policy_version_not_found",
	); err != nil {
		return err
	}
	return nil
}

func checkEnforceHandlingPolicy(client *http.Client, baseURL string) error {
	specimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return err
	}

	// No policy published yet: evaluation has a deterministic conflict result.
	if err := expectError(
		client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/plans",
		validPlanRequest(80, "可逆树脂", ptr(3.0)),
		http.StatusConflict, "handling_policy_missing",
	); err != nil {
		return err
	}

	// Publish v1: 粉砂岩/low allows adhesive 可逆树脂, exposure 30..90, peel <= 4.
	v1 := []contracts.HandlingRuleRequest{{
		MatrixKind:        "粉砂岩",
		RiskLevel:         "low",
		MaxPeelStrength:   ptr(4.0),
		AllowedAdhesives:  []string{"可逆树脂"},
		MinTargetExposure: 30,
		MaxTargetExposure: 90,
	}}
	if _, err := saveAndPublish(client, baseURL, v1); err != nil {
		return err
	}

	// Out-of-range exposure.
	if err := expectError(
		client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/plans",
		validPlanRequest(95, "可逆树脂", ptr(3.0)),
		http.StatusBadRequest, "target_exposure_out_of_range",
	); err != nil {
		return err
	}
	// Disallowed adhesive.
	if err := expectError(
		client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/plans",
		validPlanRequest(80, "瞬间胶", ptr(3.0)),
		http.StatusBadRequest, "adhesive_not_allowed",
	); err != nil {
		return err
	}
	// Peel strength beyond the cap.
	if err := expectError(
		client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/plans",
		validPlanRequest(80, "可逆树脂", ptr(5.0)),
		http.StatusBadRequest, "peel_strength_exceeds_limit",
	); err != nil {
		return err
	}
	// Missing peel strength when the rule caps it.
	if err := expectError(
		client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/plans",
		validPlanRequest(80, "可逆树脂", nil),
		http.StatusBadRequest, "peel_strength_required",
	); err != nil {
		return err
	}

	// Compliant parameters create a plan snapshotting policy v1.
	var plan planView
	if err := requestJSON(
		client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/plans",
		validPlanRequest(80, "可逆树脂", ptr(3.0)),
		http.StatusCreated, &plan,
	); err != nil {
		return err
	}
	if plan.PolicyVersion != 1 {
		return fmt.Errorf("expected plan policy snapshot 1, got %d", plan.PolicyVersion)
	}
	if err := requestJSON(
		client, http.MethodPost, baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK, &plan,
	); err != nil {
		return err
	}

	// Publish v2 tightening the exposure cap to 60 while v1 work is in flight.
	v2 := []contracts.HandlingRuleRequest{{
		MatrixKind:        "粉砂岩",
		RiskLevel:         "low",
		MaxPeelStrength:   ptr(4.0),
		AllowedAdhesives:  []string{"可逆树脂"},
		MinTargetExposure: 30,
		MaxTargetExposure: 60,
	}}
	if _, err := saveAndPublish(client, baseURL, v2); err != nil {
		return err
	}

	// The v2 release must not retroactively block the v1 plan: progress still
	// completes and the snapshot keeps referencing version 1.
	if err := recordProgress(client, baseURL, plan.ID, 100, "表面加固"); err != nil {
		return err
	}
	var snapshot planSnapshot
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+plan.ID, nil, http.StatusOK, &snapshot); err != nil {
		return err
	}
	if snapshot.Plan.Status != "completed" {
		return fmt.Errorf("expected v1 plan still completable, got %s", snapshot.Plan.Status)
	}
	if snapshot.Plan.PolicyVersion != 1 {
		return fmt.Errorf("policy update rewrote existing plan snapshot to %d", snapshot.Plan.PolicyVersion)
	}

	// New plans are evaluated against v2: exposure 80 is now out of range.
	other, err := registerSpecimenAs(client, baseURL, "PPS-2026-002", "粉砂岩", "low")
	if err != nil {
		return err
	}
	if err := expectError(
		client, http.MethodPost, baseURL+"/specimens/"+other.ID+"/plans",
		validPlanRequest(80, "可逆树脂", ptr(3.0)),
		http.StatusBadRequest, "target_exposure_out_of_range",
	); err != nil {
		return err
	}

	// Before any plan exists the current classification can be corrected.
	var reclassified specimenView
	if err := requestJSON(
		client, http.MethodPost, baseURL+"/specimens/"+other.ID+"/reclassification",
		contracts.ReclassifySpecimenRequest{MatrixKind: "灰岩", RiskLevel: "medium"},
		http.StatusOK, &reclassified,
	); err != nil {
		return err
	}
	if reclassified.MatrixKind != "灰岩" || reclassified.RiskLevel != "medium" {
		return errors.New("reclassification did not update the current classification")
	}
	if err := requestJSON(
		client, http.MethodPost, baseURL+"/specimens/"+other.ID+"/reclassification",
		contracts.ReclassifySpecimenRequest{MatrixKind: "粉砂岩", RiskLevel: "low"},
		http.StatusOK, new(specimenView),
	); err != nil {
		return err
	}

	// A classification with no published rule yields a deterministic conflict.
	limestone, err := registerSpecimenAs(client, baseURL, "PPS-2026-003", "灰岩", "medium")
	if err != nil {
		return err
	}
	if err := expectError(
		client, http.MethodPost, baseURL+"/specimens/"+limestone.ID+"/plans",
		validPlanRequest(40, "可逆树脂", ptr(3.0)),
		http.StatusConflict, "handling_rule_missing",
	); err != nil {
		return err
	}

	// Once an open plan exists, reclassification is refused so active work
	// keeps the classification key it was evaluated under.
	var otherPlan planView
	if err := requestJSON(
		client, http.MethodPost, baseURL+"/specimens/"+other.ID+"/plans",
		validPlanRequest(40, "可逆树脂", ptr(3.0)),
		http.StatusCreated, &otherPlan,
	); err != nil {
		return err
	}
	if otherPlan.PolicyVersion != 2 {
		return fmt.Errorf("expected new plan snapshot 2, got %d", otherPlan.PolicyVersion)
	}
	if err := expectError(
		client, http.MethodPost, baseURL+"/specimens/"+other.ID+"/reclassification",
		contracts.ReclassifySpecimenRequest{MatrixKind: "粉砂岩", RiskLevel: "high"},
		http.StatusConflict, "open_plan_blocks_reclassification",
	); err != nil {
		return err
	}
	return nil
}

// checkConcurrentPolicyEvaluation races plan creation against policy
// publishing. The deterministic requirements are: every plan outcome is
// either a clear conflict or a creation snapshotting exactly one existing
// version, every snapshot stays valid forever, and published versions never
// reuse or skip a number.
func checkConcurrentPolicyEvaluation(client *http.Client, baseURL string) error {
	if _, err := publishStandardPolicy(client, baseURL); err != nil {
		return err
	}

	const specimenCount = 20
	const publisherCount = 6

	var specimens []specimenView
	for i := 0; i < specimenCount; i++ {
		catalog := fmt.Sprintf("PPS-2026-%03d", i+10)
		specimen, err := registerSpecimenAs(client, baseURL, catalog, "粉砂岩", "low")
		if err != nil {
			return err
		}
		specimens = append(specimens, specimen)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	snapshots := make(map[string]int)
	var creatorFailures []error

	// Phase 1: one creator per specimen races the publishers; every creation
	// must land deterministically on one existing published version.
	for _, specimen := range specimens {
		wg.Add(1)
		go func(specimenID string) {
			defer wg.Done()
			<-start
			var created planView
			err := requestJSON(
				client, http.MethodPost, baseURL+"/specimens/"+specimenID+"/plans",
				validPlanRequest(80, "可逆树脂", nil),
				http.StatusCreated, &created,
			)
			resultMu.Lock()
			defer resultMu.Unlock()
			if err != nil {
				creatorFailures = append(creatorFailures, fmt.Errorf("specimen %s: %w", specimenID, err))
				return
			}
			snapshots[specimenID] = created.PolicyVersion
		}(specimen.ID)
	}

	// Publishers repeatedly save a new draft and publish. Identical rules keep
	// every published version compliant, so creator outcomes stay deterministic.
	var publishMu sync.Mutex
	published := make([]int, 0, publisherCount*3)
	rejectedCount := 0
	var publisherFailures []error
	for i := 0; i < publisherCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for attempt := 0; attempt < 3; attempt++ {
				if err := requestJSON(
					client, http.MethodPut, baseURL+"/policies/handling/draft",
					contracts.SavePolicyDraftRequest{UpdatedBy: "lead-he", Rules: []contracts.HandlingRuleRequest{standardRule()}},
					http.StatusOK, new(draftView),
				); err != nil {
					continue
				}
				var version policyView
				err := requestJSON(
					client, http.MethodPost, baseURL+"/policies/handling/publish",
					contracts.PublishPolicyRequest{PublishedBy: "lead-he"},
					http.StatusCreated, &version,
				)
				publishMu.Lock()
				switch {
				case err == nil:
					published = append(published, version.Version)
				case strings.Contains(err.Error(), "409"):
					// Another publisher consumed the draft between save and
					// publish: the conflict has a defined result.
					rejectedCount++
				default:
					publisherFailures = append(publisherFailures, err)
				}
				publishMu.Unlock()
			}
		}()
	}

	close(start)
	wg.Wait()

	if len(creatorFailures) > 0 {
		return fmt.Errorf("concurrent plan creation failed: %w", creatorFailures[0])
	}
	if len(publisherFailures) > 0 {
		return fmt.Errorf("concurrent publish failed unexpectedly: %w", publisherFailures[0])
	}
	if len(published) == 0 {
		return errors.New("concurrent publishers never produced a new version")
	}

	// Published versions must be a dense, increasing sequence starting at 2
	// (version 1 was the initial setup publish), with no duplicates.
	sort.Ints(published)
	for i, version := range published {
		if version != i+2 {
			return fmt.Errorf("published versions not dense: got %v", published)
		}
	}
	finalVersion := published[len(published)-1]

	// Every created plan referenced a version that still exists, and each
	// specimen ended with exactly one open plan.
	if len(snapshots) != specimenCount {
		return fmt.Errorf("expected %d plan snapshots, got %d", specimenCount, len(snapshots))
	}
	for specimenID, version := range snapshots {
		if version < 1 || version > finalVersion {
			return fmt.Errorf("plan for %s snapshotted unknown version %d", specimenID, version)
		}
		var fetched policyView
		if err := requestJSON(client, http.MethodGet, baseURL+"/policies/handling/versions/"+strconv.Itoa(version), nil, http.StatusOK, &fetched); err != nil {
			return fmt.Errorf("snapshotted policy %d disappeared: %w", version, err)
		}
		listURL := baseURL + "/specimens/" + specimenID + "/plans"
		var listing struct {
			Items []planView `json:"items"`
		}
		if err := requestJSON(client, http.MethodGet, listURL, nil, http.StatusOK, &listing); err != nil {
			return err
		}
		open := 0
		for _, item := range listing.Items {
			if item.Status == "draft" || item.Status == "approved" || item.Status == "active" {
				open++
			}
		}
		if open != 1 {
			return fmt.Errorf("specimen %s ended with %d open plans", specimenID, open)
		}
	}
	return nil
}

func standardRule() contracts.HandlingRuleRequest {
	return contracts.HandlingRuleRequest{
		MatrixKind:        "粉砂岩",
		RiskLevel:         "low",
		AllowedAdhesives:  []string{"可逆树脂"},
		MinTargetExposure: 1,
		MaxTargetExposure: 100,
	}
}

func publishStandardPolicy(client *http.Client, baseURL string) (policyView, error) {
	return saveAndPublish(client, baseURL, []contracts.HandlingRuleRequest{standardRule()})
}

func saveAndPublish(
	client *http.Client,
	baseURL string,
	rules []contracts.HandlingRuleRequest,
) (policyView, error) {
	var policy policyView
	if err := requestJSON(
		client, http.MethodPut, baseURL+"/policies/handling/draft",
		contracts.SavePolicyDraftRequest{UpdatedBy: "lead-he", Rules: rules},
		http.StatusOK, new(draftView),
	); err != nil {
		return policy, err
	}
	err := requestJSON(
		client, http.MethodPost, baseURL+"/policies/handling/publish",
		contracts.PublishPolicyRequest{PublishedBy: "lead-he"},
		http.StatusCreated, &policy,
	)
	return policy, err
}

func validPlanRequest(exposure int, adhesive string, peel *float64) contracts.CreatePlanRequest {
	return contracts.CreatePlanRequest{
		Objective:      "暴露主要关节面",
		Method:         "手工显微剥离",
		Adhesive:       adhesive,
		TargetExposure: exposure,
		PeelStrength:   peel,
	}
}

func ptr(value float64) *float64 {
	return &value
}

func registerSpecimen(client *http.Client, baseURL string) (specimenView, error) {
	return registerSpecimenAs(client, baseURL, "PPS-2026-001", "粉砂岩", "low")
}

func registerSpecimenAs(
	client *http.Client,
	baseURL string,
	catalog string,
	matrix string,
	risk string,
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
	var plan planView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimenID+"/plans",
		contracts.CreatePlanRequest{
			Objective:      "暴露主要关节面",
			Method:         "手工显微剥离",
			Adhesive:       "可逆树脂",
			TargetExposure: 80,
		},
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

func expectError(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
	wantCode string,
) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("request %s %s returned %d (want %d): %s", method, url, response.StatusCode, wantStatus, string(raw))
	}
	var decoded errorView
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("decode error body for %s %s: %w", method, url, err)
	}
	if decoded.Code != wantCode {
		return fmt.Errorf("request %s %s returned code %q, want %q", method, url, decoded.Code, wantCode)
	}
	return nil
}

func requestJSON(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
	target any,
) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("request %s %s returned %d: %s", method, url, response.StatusCode, string(raw))
	}
	if target == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode response for %s %s: %w", method, url, err)
	}
	return nil
}
