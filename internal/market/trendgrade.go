package market

const dipFloor = 0.15

const dipCeiling = 0.60

const streakFull = 12

func DipGrade(offHigh float64) float64 { return grade(offHigh, dipFloor, dipCeiling) }

func StreakGrade(ups int) float64 { return grade(float64(ups), 0, streakFull) }
