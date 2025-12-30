package cluster

import (
	"testing"
)

func TestRoleString(t *testing.T) {
	tests := []struct {
		role Role
		want string
	}{
		{RolePassive, "PASSIVE"},
		{RolePrimary, "PRIMARY"},
		{Role(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.role.String(); got != tt.want {
				t.Errorf("Role.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRoleIsPrimary(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want bool
	}{
		{"primary is primary", RolePrimary, true},
		{"passive is not primary", RolePassive, false},
		{"unknown is not primary", Role(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.role.IsPrimary(); got != tt.want {
				t.Errorf("Role.IsPrimary() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRoleIsPassive(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want bool
	}{
		{"passive is passive", RolePassive, true},
		{"primary is not passive", RolePrimary, false},
		{"unknown is not passive", Role(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.role.IsPassive(); got != tt.want {
				t.Errorf("Role.IsPassive() = %v, want %v", got, tt.want)
			}
		})
	}
}
