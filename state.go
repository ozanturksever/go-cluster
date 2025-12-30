package cluster

// Role represents the role of a node in the cluster.
type Role int

const (
	// RolePassive indicates the node is a passive/standby node.
	RolePassive Role = iota
	// RolePrimary indicates the node is the active/leader node.
	RolePrimary
)

// String returns the string representation of the role.
func (r Role) String() string {
	switch r {
	case RolePassive:
		return "PASSIVE"
	case RolePrimary:
		return "PRIMARY"
	default:
		return "UNKNOWN"
	}
}

// IsPrimary returns true if the role is primary/leader.
func (r Role) IsPrimary() bool {
	return r == RolePrimary
}

// IsPassive returns true if the role is passive/standby.
func (r Role) IsPassive() bool {
	return r == RolePassive
}
