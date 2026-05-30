package entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeUserRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role string
		want string
	}{
		{name: "user", role: " user ", want: UserRoleUser},
		{name: "editor", role: "EDITOR", want: UserRoleEditor},
		{name: "admin", role: "Admin", want: UserRoleAdmin},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeUserRole(tt.role)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeUserRoleRejectsInvalidRole(t *testing.T) {
	t.Parallel()

	_, err := NormalizeUserRole("owner")

	require.ErrorIs(t, err, ErrInvalidRole)
}

func TestEditorialRolePermissions(t *testing.T) {
	t.Parallel()

	assert.True(t, CanReviewEditorial(UserRoleEditor))
	assert.True(t, CanReviewEditorial(UserRoleAdmin))
	assert.False(t, CanReviewEditorial(UserRoleUser))

	assert.False(t, CanPublishEditorial(UserRoleEditor))
	assert.True(t, CanPublishEditorial(UserRoleAdmin))
}
