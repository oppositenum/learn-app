package auth

import "testing"

func TestPasswordHashRoundTripAndPolicy(t *testing.T) {
	hash, err := HashPassword("a sufficiently long test password")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "a sufficiently long test password" || !VerifyPassword(hash, "a sufficiently long test password") {
		t.Fatal("password hash did not verify")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("wrong password verified")
	}
	if _, err := HashPassword("ab"); err == nil {
		t.Fatal("short password accepted")
	}
}
