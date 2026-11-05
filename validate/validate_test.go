package validate

import "testing"

func TestEmailAndUsername(t *testing.T) {
	for _, ok := range []string{"a@b.co", "first.last+tag@example.com"} {
		if !IsValidEmail(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "a@b", "a b@c.com", "@x.com"} {
		if IsValidEmail(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
	if !IsValidUsername("a_b-1") || IsValidUsername("a b") {
		t.Error("username rules wrong")
	}
}

func TestPasswordStrength(t *testing.T) {
	if errs := PasswordStrengthErrors("Abcdef1!"); len(errs) != 0 {
		t.Errorf("strong password rejected: %v", errs)
	}
	if errs := PasswordStrengthErrors("abc"); len(errs) != 4 {
		t.Errorf("want 4 unmet rules, got %v", errs)
	}
}

func TestHashRoundTrip(t *testing.T) {
	h, err := HashPassword("Abcdef1!")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPasswordHash("Abcdef1!", h) || CheckPasswordHash("wrong", h) {
		t.Error("hash check wrong")
	}
}

func TestBurnPasswordCheckDoesNotPanic(t *testing.T) {
	BurnPasswordCheck("anything")
	BurnPasswordCheck("")
}
