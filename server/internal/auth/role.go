package auth

import "fmt"

type Role string

const (
	RoleStudent Role = "STUDENT"
	RoleParent  Role = "PARENT"
	RoleOwner   Role = "OWNER"
)

func (role Role) Valid() bool {
	switch role {
	case RoleStudent, RoleParent, RoleOwner:
		return true
	default:
		return false
	}
}

func ParseRole(value string) (Role, error) {
	role := Role(value)
	if !role.Valid() {
		return "", fmt.Errorf("unsupported role %q", value)
	}
	return role, nil
}
