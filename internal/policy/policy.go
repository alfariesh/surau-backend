// Package policy is the single authorization decision point (A-1): it maps
// roles to named capabilities. Access-control layers (middleware, usecases)
// ask policy.Can(role, capability) and never compare role strings themselves
// — that invariant is enforced by internal/policy/no_role_compare_test.go.
//
// Form is locked by decision A-D3: capabilities-in-code with one policy point,
// NOT a dynamic DB RBAC table and NOT an external policy engine.
package policy

import (
	"strings"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// Capability is a named permission. Values are stable kebab-case strings; they
// appear in audit metadata and the FE contract doc, so an existing value must
// never change.
type Capability string

const (
	// CapReviewEditorial: access editorial review and draft-authoring tools.
	CapReviewEditorial Capability = "review-editorial"
	// CapPublishProduction: publish/unpublish production content and delete
	// final assets (high-class destructive; also step-up gated).
	CapPublishProduction Capability = "publish-production"
	// CapManageUsers: manage accounts, roles, and admin email operations.
	CapManageUsers Capability = "manage-users"
	// CapCurateEntities: curate knowledge entities (declared; wired in W-0/W-6).
	CapCurateEntities Capability = "curate-entities"
	// CapApproveNeutralClaim: approve non-sensitive wiki claims (declared; W-0/W-5).
	CapApproveNeutralClaim Capability = "approve-neutral-claim"
	// CapApproveSensitiveClaim: approve sensitive/contested claims — scholar
	// gate (declared; W-0/W-5). A-1 AC: only scholar_reviewer + admin.
	CapApproveSensitiveClaim Capability = "approve-sensitive-claim"
	// CapManageServiceTokens: manage scoped machine tokens (declared; A-2).
	CapManageServiceTokens Capability = "manage-service-tokens"
)

// roleCapabilities is the FROZEN role→capability matrix. admin is the superset
// (handled in Can, not listed here). A capability may be ADDED to a role, but
// the shape is pinned by the golden twin in policy_test.go — changing a row
// without updating that twin fails the contract test.
//
// Conservative for A-1: the three live gates (review-editorial, publish-
// production, manage-users) keep exactly today's behavior; the other four are
// declared for the wiki/machine-token waves and have no live route yet.
//
//nolint:gochecknoglobals // immutable policy table, read-only at runtime
var roleCapabilities = map[string][]Capability{
	entity.UserRoleUser: {},
	entity.UserRoleEditor: {
		CapReviewEditorial,
	},
	entity.UserRoleCurator: {
		CapCurateEntities,
		CapApproveNeutralClaim,
	},
	entity.UserRoleScholarReviewer: {
		CapCurateEntities,
		CapApproveNeutralClaim,
		CapApproveSensitiveClaim,
	},
	// admin is the superset — see Can. Listed with nil so completeness tests
	// see every valid role as a matrix key.
	entity.UserRoleAdmin: nil,
}

// mfaMandatedRoles are the roles required to enroll MFA (A-3 O-2-1 default a):
// admin + scholar_reviewer. Optional for editor/curator.
//
//nolint:gochecknoglobals // immutable policy set
var mfaMandatedRoles = map[string]bool{
	entity.UserRoleAdmin:           true,
	entity.UserRoleScholarReviewer: true,
}

// Can reports whether role holds capability. admin holds every capability
// (superset); unknown roles hold none.
func Can(role string, capability Capability) bool {
	role = normalize(role)

	if role == entity.UserRoleAdmin {
		return true
	}

	for _, held := range roleCapabilities[role] {
		if held == capability {
			return true
		}
	}

	return false
}

// Capabilities returns the capabilities a role holds. admin returns the full
// set (every declared capability). The result is a fresh slice per call.
func Capabilities(role string) []Capability {
	role = normalize(role)

	if role == entity.UserRoleAdmin {
		return allCapabilities()
	}

	held := roleCapabilities[role]
	out := make([]Capability, len(held))
	copy(out, held)

	return out
}

// RoleRequiresMFA reports whether the role is under the MFA enrollment mandate
// (A-1 subsumes the former entity helper). admin + scholar_reviewer.
func RoleRequiresMFA(role string) bool {
	return mfaMandatedRoles[normalize(role)]
}

// AllCapabilities is the full declared capability set, for tests/introspection.
func AllCapabilities() []Capability {
	return allCapabilities()
}

func allCapabilities() []Capability {
	return []Capability{
		CapReviewEditorial,
		CapPublishProduction,
		CapManageUsers,
		CapCurateEntities,
		CapApproveNeutralClaim,
		CapApproveSensitiveClaim,
		CapManageServiceTokens,
	}
}

func normalize(role string) string {
	return strings.ToLower(strings.TrimSpace(role))
}
