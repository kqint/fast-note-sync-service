package syncgate

import "testing"

func TestPauseResume(t *testing.T) {
	const uid = 42
	const vault = "test-vault"

	if IsPaused(uid, vault) {
		t.Fatal("vault should not be paused initially")
	}

	Pause(uid, vault)
	if !IsPaused(uid, vault) {
		t.Fatal("vault should be paused after Pause")
	}

	// Other vaults/users are unaffected
	if IsPaused(uid, "other") || IsPaused(43, vault) {
		t.Fatal("pause leaked to other vault/user")
	}

	Resume(uid, vault)
	if IsPaused(uid, vault) {
		t.Fatal("vault should be resumed after Resume")
	}

	// Resume is idempotent
	Resume(uid, vault)
}
