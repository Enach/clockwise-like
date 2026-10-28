package api

// PAC-25, AC-15: checkable facts of the contract bundle for the two operations
// PAC-25 changes. The prose (who may read a link, when a removal is refused)
// is a reviewer check, not this test's. The bundle is read with the parser in
// pac21_contract_5xx_test.go.

import "testing"

func TestPAC25ContractGetSchedulingLink(t *testing.T) {
	root := parseBundle(t, pac21Bundle)
	op := root.get("paths", "/api/scheduling-links/{id}", "get")
	if op == nil {
		t.Fatal("bundle has no GET /api/scheduling-links/{id}")
	}
	if id := op.get("operationId"); id == nil || id.scalar != "getSchedulingLink" {
		t.Fatalf("operationId = %v, want getSchedulingLink", id)
	}
	if op.get("x-uncertain") != nil {
		t.Error("getSchedulingLink still carries x-uncertain (U-08)")
	}
	if op.get("responses", "403") != nil {
		t.Error("getSchedulingLink declares a 403; a refused read is the 404 (PAC-25 D-1)")
	}
	if op.get("responses", "404") == nil {
		t.Error("getSchedulingLink declares no 404")
	}
}

func TestPAC25ContractRemoveTeamMember422(t *testing.T) {
	root := parseBundle(t, pac21Bundle)
	op := root.get("paths", "/api/teams/{id}/members/{userId}", "delete")
	if op == nil {
		t.Fatal("bundle has no DELETE /api/teams/{id}/members/{userId}")
	}
	if id := op.get("operationId"); id == nil || id.scalar != "removeTeamMember" {
		t.Fatalf("operationId = %v, want removeTeamMember", id)
	}
	media := op.get("responses", "422", "content", "application/json")
	if media == nil {
		t.Fatal("removeTeamMember declares no 422 application/json response")
	}
	if name := refName(media.get("schema"), "#/components/schemas/"); name != "ErrorResponse" {
		t.Errorf("422 schema = %q, want ErrorResponse", name)
	}
	if ex := media.get("example", "error"); ex == nil || ex.scalar != "the team owner cannot be removed" {
		t.Errorf("422 example error = %v, want \"the team owner cannot be removed\"", ex)
	}
}
