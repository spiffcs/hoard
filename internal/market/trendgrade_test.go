package market

import "testing"

func TestDipGradeRewardsADeeperDiscount(t *testing.T) {
	if g := DipGrade(0.10); g != 0 {
		t.Errorf("DipGrade(0.10) = %v, want the dull end below the floor", g)
	}
	if g := DipGrade(dipFloor); g != 0 {
		t.Errorf("DipGrade(floor) = %v, want exactly 0 at the floor", g)
	}
	if g := DipGrade(dipCeiling); g != 1 {
		t.Errorf("DipGrade(ceiling) = %v, want the full-green end", g)
	}
	if g := DipGrade(0.90); g != 1 {
		t.Errorf("DipGrade(0.90) = %v, want clamped past the ceiling", g)
	}
	mid := (dipFloor + dipCeiling) / 2
	if g := DipGrade(mid); g <= 0 || g >= 1 {
		t.Errorf("DipGrade(mid) = %v, want a value between the ends", g)
	}
	if DipGrade(0.30) >= DipGrade(0.45) {
		t.Error("DipGrade must rise with depth")
	}
}

func TestStreakGradeRewardsALongerRun(t *testing.T) {
	if g := StreakGrade(0); g != 0 {
		t.Errorf("StreakGrade(0) = %v, want the dull end", g)
	}
	if g := StreakGrade(streakFull); g != 1 {
		t.Errorf("StreakGrade(full) = %v, want the full-green end", g)
	}
	if g := StreakGrade(streakFull + 20); g != 1 {
		t.Errorf("StreakGrade past full = %v, want clamped", g)
	}
	if StreakGrade(3) >= StreakGrade(9) {
		t.Error("StreakGrade must rise with the run length")
	}
}
