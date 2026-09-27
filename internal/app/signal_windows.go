package app

// foreground is always true on Windows, which has no job control: no process
// group is stopped for setting the console's mode.
func foreground(int) bool { return true }
