package station

import (
	"strconv"

	"example.com/paleo-prep-station/internal/contracts"
)

func notFoundSpecimen(id string) error {
	return contracts.NotFound("specimen_not_found", "未找到标本 "+id)
}

func notFoundPlan(id string) error {
	return contracts.NotFound("plan_not_found", "未找到修复方案 "+id)
}

func notFoundPolicyVersion(version int) error {
	return contracts.NotFound("policy_version_not_found", "未找到处置限制政策版本 "+strconv.Itoa(version))
}
