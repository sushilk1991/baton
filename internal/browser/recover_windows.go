package browser

// Windows builds the lifecycle but lacks the process birth-identity driver.
// Never signal a persisted PID without that proof.
func saveOwner(string, string, *child) error { return nil }
func orphaned(string) error                  { return errStopped }
func recoverChild(string) error              { return nil }
