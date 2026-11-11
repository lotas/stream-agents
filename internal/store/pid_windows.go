package store

// pidAlive cannot check Claude's Unix pids on Windows; assume running so the
// age windows decide.
func pidAlive(int) bool { return true }
