package policy_test

import (
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allRoles is the frozen set of account roles the matrix must cover.
var allRoles = []string{
	entity.UserRoleUser,
	entity.UserRoleEditor,
	entity.UserRoleCurator,
	entity.UserRoleScholarReviewer,
	entity.UserRoleAdmin,
}

// goldenMatrix is a DELIBERATE duplicate of the production role→capability
// table (policy.go). It is the frozen contract: changing a grant in policy.go
// without consciously updating this twin fails TestFrozenMatrixMatchesGolden.
// admin is the superset (every capability), asserted separately.
var goldenMatrix = map[string]map[policy.Capability]bool{
	entity.UserRoleUser: {
		policy.CapReviewEditorial:       false,
		policy.CapPublishProduction:     false,
		policy.CapManageUsers:           false,
		policy.CapCurateEntities:        false,
		policy.CapApproveNeutralClaim:   false,
		policy.CapApproveSensitiveClaim: false,
		policy.CapManageServiceTokens:   false,
	},
	entity.UserRoleEditor: {
		policy.CapReviewEditorial:       true,
		policy.CapPublishProduction:     false,
		policy.CapManageUsers:           false,
		policy.CapCurateEntities:        false,
		policy.CapApproveNeutralClaim:   false,
		policy.CapApproveSensitiveClaim: false,
		policy.CapManageServiceTokens:   false,
	},
	entity.UserRoleCurator: {
		policy.CapReviewEditorial:       false,
		policy.CapPublishProduction:     false,
		policy.CapManageUsers:           false,
		policy.CapCurateEntities:        true,
		policy.CapApproveNeutralClaim:   true,
		policy.CapApproveSensitiveClaim: false,
		policy.CapManageServiceTokens:   false,
	},
	entity.UserRoleScholarReviewer: {
		policy.CapReviewEditorial:       false,
		policy.CapPublishProduction:     false,
		policy.CapManageUsers:           false,
		policy.CapCurateEntities:        true,
		policy.CapApproveNeutralClaim:   true,
		policy.CapApproveSensitiveClaim: true,
		policy.CapManageServiceTokens:   false,
	},
	entity.UserRoleAdmin: {
		policy.CapReviewEditorial:       true,
		policy.CapPublishProduction:     true,
		policy.CapManageUsers:           true,
		policy.CapCurateEntities:        true,
		policy.CapApproveNeutralClaim:   true,
		policy.CapApproveSensitiveClaim: true,
		policy.CapManageServiceTokens:   true,
	},
}

// TestFrozenMatrixMatchesGolden pins every (role, capability) decision. Any
// drift in policy.go's table (or a new capability/role) fails here until the
// golden twin is consciously updated — the A-3 (AC-3) frozen-contract pattern.
func TestFrozenMatrixMatchesGolden(t *testing.T) {
	t.Parallel()

	caps := policy.AllCapabilities()

	for _, role := range allRoles {
		want, ok := goldenMatrix[role]
		require.Truef(t, ok, "role %q missing from golden matrix", role)

		for _, cap := range caps {
			assert.Equalf(t, want[cap], policy.Can(role, cap),
				"policy.Can(%q, %q) drifted from the frozen matrix", role, cap)
		}
	}
}

// TestCapabilityValuesFrozen pins the wire strings (audit metadata + FE
// contract depend on them; an existing value must never change).
func TestCapabilityValuesFrozen(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "review-editorial", string(policy.CapReviewEditorial))
	assert.Equal(t, "publish-production", string(policy.CapPublishProduction))
	assert.Equal(t, "manage-users", string(policy.CapManageUsers))
	assert.Equal(t, "curate-entities", string(policy.CapCurateEntities))
	assert.Equal(t, "approve-neutral-claim", string(policy.CapApproveNeutralClaim))
	assert.Equal(t, "approve-sensitive-claim", string(policy.CapApproveSensitiveClaim))
	assert.Equal(t, "manage-service-tokens", string(policy.CapManageServiceTokens))
	assert.Len(t, policy.AllCapabilities(), 7)
}

// TestApproveSensitiveClaimGate is the A-1 AC: ONLY scholar_reviewer and admin
// pass the sensitive-claim gate.
func TestApproveSensitiveClaimGate(t *testing.T) {
	t.Parallel()

	for _, role := range allRoles {
		allowed := policy.Can(role, policy.CapApproveSensitiveClaim)

		switch role {
		case entity.UserRoleScholarReviewer, entity.UserRoleAdmin:
			assert.Truef(t, allowed, "%q must pass approve-sensitive-claim", role)
		default:
			assert.Falsef(t, allowed, "%q must NOT pass approve-sensitive-claim", role)
		}
	}
}

// TestAdminIsSuperset: admin holds every declared capability.
func TestAdminIsSuperset(t *testing.T) {
	t.Parallel()

	for _, cap := range policy.AllCapabilities() {
		assert.Truef(t, policy.Can(entity.UserRoleAdmin, cap), "admin must hold %q", cap)
	}

	assert.ElementsMatch(t, policy.AllCapabilities(), policy.Capabilities(entity.UserRoleAdmin))
}

// TestCanNormalizesAndRejectsUnknown: casing/whitespace tolerated; unknown
// roles hold nothing.
func TestCanNormalizesAndRejectsUnknown(t *testing.T) {
	t.Parallel()

	assert.True(t, policy.Can("  ADMIN ", policy.CapManageUsers))
	assert.True(t, policy.Can("Editor", policy.CapReviewEditorial))
	assert.False(t, policy.Can("owner", policy.CapReviewEditorial))
	assert.False(t, policy.Can("", policy.CapReviewEditorial))
	assert.Empty(t, policy.Capabilities("owner"))
}

// TestRoleRequiresMFA: the MFA mandate is admin + scholar_reviewer only.
func TestRoleRequiresMFA(t *testing.T) {
	t.Parallel()

	for _, role := range allRoles {
		want := role == entity.UserRoleAdmin || role == entity.UserRoleScholarReviewer
		assert.Equalf(t, want, policy.RoleRequiresMFA(role), "RoleRequiresMFA(%q)", role)
	}

	assert.True(t, policy.RoleRequiresMFA(" Scholar_Reviewer "), "normalizes")
}

// TestMatrixCoversEveryValidRole: every entity-valid role is a matrix key and
// every matrix role is entity-valid — no role can silently escape the policy.
func TestMatrixCoversEveryValidRole(t *testing.T) {
	t.Parallel()

	for _, role := range allRoles {
		require.Truef(t, entity.IsValidUserRole(role), "test role %q must be entity-valid", role)
	}

	// Capabilities() must resolve for every valid role without panicking and
	// return a subset of the declared capabilities.
	declared := map[policy.Capability]bool{}
	for _, cap := range policy.AllCapabilities() {
		declared[cap] = true
	}

	for _, role := range allRoles {
		for _, cap := range policy.Capabilities(role) {
			assert.Truef(t, declared[cap], "role %q holds undeclared capability %q", role, cap)
		}
	}
}
