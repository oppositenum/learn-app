package auth

import "testing"

func TestParseRole(t *testing.T) {
	for _, value := range []string{"STUDENT", "PARENT", "OWNER"} {
		role, err := ParseRole(value)
		if err != nil {
			t.Fatalf("ParseRole(%q): %v", value, err)
		}
		if string(role) != value {
			t.Fatalf("expected %q, got %q", value, role)
		}
	}

	if _, err := ParseRole("TEACHER"); err == nil {
		t.Fatal("expected unsupported role to fail")
	}
}
