package flag

import "testing"

// A FLAG FLIP IS A SHIP.
//
// FEATURES.md claimed "flag → deploy marker: auto-recorded" under "Only we have" while the code
// comment said "a future increment records this flip". The claim sat in the strongest-worded
// part of the comparison table and was simply false. Making it true beat deleting it: flipping a
// flag is often the only ship a team makes between releases, and it is the one kind this tool
// can detect with no CI setup at all.
func TestFlippingAFlagFiresTheRecorder(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir + "/flags.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(Flag{Key: "checkout_v2", Enabled: false}); err != nil {
		t.Fatal(err)
	}

	var flips []string
	s.OnFlip(func(key string, on bool) {
		state := "off"
		if on {
			state = "on"
		}
		flips = append(flips, key+"="+state)
	})

	if _, err := s.SetEnabled("checkout_v2", true); err != nil {
		t.Fatal(err)
	}
	if len(flips) != 1 || flips[0] != "checkout_v2=on" {
		t.Fatalf("a real flip did not reach the recorder: %v", flips)
	}

	// A NO-OP FLIP IS NOT A SHIP. Turning an already-on flag on happens constantly — a retry, an
	// idempotent config apply, a double-clicked toggle — and recording each one would fill the
	// deploy list with releases that never happened and hand the investigation ships to blame
	// that do not exist.
	if _, err := s.SetEnabled("checkout_v2", true); err != nil {
		t.Fatal(err)
	}
	if len(flips) != 1 {
		t.Fatalf("a no-op flip was recorded as a ship: %v", flips)
	}

	// And turning it back off is a ship again.
	if _, err := s.SetEnabled("checkout_v2", false); err != nil {
		t.Fatal(err)
	}
	if len(flips) != 2 || flips[1] != "checkout_v2=off" {
		t.Fatalf("turning a flag off was not recorded: %v", flips)
	}
}

// No recorder wired — a self-hosted instance with no deploy store — must behave exactly as
// before rather than panicking on a nil hook.
func TestFlippingWithNoRecorderIsSafe(t *testing.T) {
	s, err := Open(t.TempDir() + "/flags.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(Flag{Key: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnabled("x", true); err != nil {
		t.Fatalf("flipping without a recorder failed: %v", err)
	}
}
